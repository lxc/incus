package uefi

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// uintSizes maps unsigned integer type specifiers to their bit size.
var uintSizes = map[string]int{"u8": 8, "u16": 16, "u32": 32, "u64": 64, "u64be": 64}

// dpFormatter helps formatting device paths with optional arguments.
type dpFormatter struct {
	name          string
	args          []string
	firstOptional int
}

// add adds an argument to a device path formatter.
func (d *dpFormatter) add(value string, optional ...bool) {
	opt := false
	if len(optional) > 0 {
		opt = optional[0]
	}

	// If the argument contains characters than can trip a device path parser, quote it.
	quote := len(value) == 0
	for _, r := range value {
		if !strconv.IsPrint(r) || slices.Contains([]rune(",()\" "), r) {
			quote = true
			break
		}
	}

	if quote {
		value = strconv.Quote(value)
	}

	d.args = append(d.args, value)
	if !opt {
		d.firstOptional = len(d.args)
	}
}

// addMandatory adds mandatory arguments to a device path formatter.
func (d *dpFormatter) addMandatory(values ...string) {
	for _, value := range values {
		d.add(value)
	}
}

// String returns the formatted device path.
func (d *dpFormatter) String() string {
	return fmt.Sprintf("%s(%s)", d.name, strings.Join(d.args[:d.firstOptional], ","))
}

// addDissected dissects and adds consecutive device path arguments.
func (d *dpFormatter) addDissected(r *reader, types ...string) error {
	for _, t := range types {
		t, loop := strings.CutSuffix(t, "*")
		parts := strings.SplitN(t, "?", 2)
		optional := len(parts) == 2
		t = parts[0]
		hasDefault := optional && parts[1] != ""
		size, err := strconv.Atoi(t)
		if err == nil {
			t = ""
		} else if strings.HasPrefix(t, "s") {
			size, err = strconv.Atoi(t[1:])
			if err != nil {
				return fmt.Errorf("Cannot parse skip length for %s: %w", t, err)
			}

			t = "s"
		}

		for !loop || !r.eof() {
			switch t {
			case "s":
				if optional {
					// This indicates a programming error and shouldn’t happen.
					return errors.New("Cannot make skips optional")
				}

				err := r.skip(size)
				if err != nil {
					return err
				}

			case "u8", "u16", "u32", "u64", "u64be":
				var v, defVal uint64
				var err error
				switch t {
				case "u8":
					var i uint8
					i, err = r.readU8()
					v = uint64(i)
				case "u16":
					var i uint16
					i, err = r.readU16()
					v = uint64(i)
				case "u32":
					var i uint32
					i, err = r.readU32()
					v = uint64(i)
				case "u64":
					v, err = r.readU64()
				case "u64be":
					v, err = r.readU64BE()
				}

				if err != nil {
					return err
				}

				if hasDefault {
					defVal, _ = strconv.ParseUint(parts[1], 10, uintSizes[t])
				}

				d.add(fmt.Sprintf("0x%x", v), optional && v == defVal)
			case "guid", "guidbe", "eisa", "eui64", "eui64be":
				var v, defVal string
				var err error
				if optional {
					if !hasDefault {
						// This indicates a programming error and shouldn’t happen.
						return fmt.Errorf("No default %s given", t)
					}

					defVal = parts[1]
				}

				switch t {
				case "guid":
					v, err = r.readGUID()
				case "guidbe":
					v, err = r.readGUIDBE()
				case "eisa":
					v, err = r.readEISA()
				case "eui64":
					v, err = r.readEUI64()
				case "eui64be":
					v, err = r.readEUI64BE()
				}

				if err != nil {
					return err
				}

				d.add(v, optional && v == defVal)
			case "z8", "zn8", "z16", "zn16":
				var v, defVal string
				var err error
				switch t {
				case "z8":
					v, err = r.readZ8(r.rem())
				case "zn8":
					v, err = r.readZn8()
				case "z16":
					v, err = r.readZ8(r.rem() / 2)
				case "zn16":
					v, err = r.readZn16()
				}

				if err != nil {
					return err
				}

				if hasDefault {
					defVal = parts[1]
				}

				d.add(v, optional && v == defVal)
			// This case handles both fixed integers and `*` specifiers to mean “read the whole buffer and
			// hex-dump it”.
			case "":
				bufSize := r.rem()
				if !loop {
					bufSize = size
				}

				if optional {
					// This indicates a programming error and shouldn’t happen.
					return errors.New("No default hex-dump")
				}

				v, err := r.read(bufSize)
				if err != nil {
					return err
				}

				// As everything is consumed, reset the loop indicator.
				loop = false
				d.add(fmt.Sprintf("%x", v), len(v) == 0)
			default:
				return fmt.Errorf("Unknown type specifier: %s", t)
			}

			if !loop {
				break
			}
		}
	}

	return nil
}
