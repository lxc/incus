package uefi

import (
	"encoding/hex"
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

// formatDPArgs formats consecutive device path arguments.
func formatDPArgs(w *writer, args []string, types ...string) error {
	var err error
	var i int
	var t string
	initialArgs := args
out:
	for i, t = range types {
		t, loop := strings.CutSuffix(t, "*")
		parts := strings.SplitN(t, "?", 2)
		optional := len(parts) == 2
		t = parts[0]
		hasDefault := optional && parts[1] != ""
		skip := optional && len(args) == 0
		if len(args) == 0 && !loop && !skip {
			err = errors.New("Not enough arguments")
			break
		}

		size, sizeErr := strconv.Atoi(t)
		if sizeErr == nil {
			t = ""
		} else if strings.HasPrefix(t, "s") {
			size, err = strconv.Atoi(t[1:])
			if err != nil {
				return fmt.Errorf("Cannot parse skip length for %s: %w", t, err)
			}

			t = "s"
		}

		for !loop || len(args) > 0 {
			skipThis := skip
			switch t {
			case "s":
				if optional {
					// This indicates a programming error and shouldn’t happen.
					return errors.New("Cannot make skips optional")
				}

				err = w.skip(size)
				skipThis = true
			case "u8", "u16", "u32", "u64", "u64be":
				var v uint64
				if hasDefault {
					v, _ = strconv.ParseUint(parts[1], 10, uintSizes[t])
				}

				if !skip {
					v, err = strconv.ParseUint(args[0], 0, uintSizes[t])
				}

				if err == nil {
					switch t {
					case "u8":
						err = w.writeU8(uint8(v))
					case "u16":
						err = w.writeU16(uint16(v))
					case "u32":
						err = w.writeU32(uint32(v))
					case "u64":
						err = w.writeU64(v)
					case "u64be":
						err = w.writeU64BE(v)
					}
				}

			case "guid", "guidbe", "eisa", "eui64", "eui64be":
				var v string
				if optional {
					if !hasDefault {
						// This indicates a programming error and shouldn’t happen.
						return fmt.Errorf("No default %s given", t)
					}

					v = parts[1]
				}

				if !skip {
					v = args[0]
				}

				switch t {
				case "guid":
					err = w.writeGUID(v)
				case "guidbe":
					err = w.writeGUIDBE(v)
				case "eisa":
					err = w.writeEISA(v)
				case "eui64":
					err = w.writeEUI64(v)
				case "eui64be":
					err = w.writeEUI64BE(v)
				}

			case "z8", "zn8", "z16", "zn16":
				var v string
				if hasDefault {
					v = parts[1]
				}

				if !skip {
					v = args[0]
				}

				switch t {
				case "z8":
					err = w.writeZ8(v)
				case "zn8":
					err = w.writeZn8(v)
				case "z16":
					err = w.writeZ16(v)
				case "zn16":
					err = w.writeZn16(v)
				}

			// This case handles both fixed integers and `*` specifiers to mean “write the argument as a
			// hex-dump”.
			case "":
				if optional {
					// This indicates a programming error and shouldn’t happen.
					return errors.New("No default hex-dump")
				}

				// A hex dump takes twice the length of the actual data.
				if !loop && len(args[0]) != 2*size {
					return fmt.Errorf("Wrong buffer size for %s (expected %d)", args[0], size)
				}

				var v []byte
				// As an extra precaution, strip any leading `0x`.
				v, err = hex.DecodeString(strings.TrimPrefix(strings.ToLower(args[0]), "0x"))
				if err == nil {
					err = w.write(v)
				}

				// To play nice with whatever comes next, reset the loop indicator.
				loop = false
			default:
				err = fmt.Errorf("Unknown type specifier: %s", t)
			}

			if err != nil {
				break out
			}

			if !skipThis {
				args = args[1:]
			}

			if !loop {
				break
			}
		}
	}

	if err != nil {
		return fmt.Errorf("Failed writing %v into %v at index %d: %w", initialArgs, types, i, err)
	}

	if len(args) != 0 {
		return tooManyArgs(args)
	}

	return nil
}

// expectArgs returns an error if the wrong number of arguments was passed.
func expectArgs(args []string, sizes ...int) error {
	sizesStr := make([]string, len(sizes))
	for i, size := range sizes {
		if len(args) == size {
			return nil
		}

		sizesStr[i] = strconv.Itoa(size)
	}

	eitherOr := ""
	switch len(sizes) {
	case 0:
		// This indicates a programming error and shouldn’t happen.
		return errors.New("Missing argument constraints")
	case 1:
		eitherOr = sizesStr[0]
	default:
		eitherOr = "either " + strings.Join(sizesStr[:len(sizes)-1], ", ") + " or " + sizesStr[len(sizes)-1]
	}

	return fmt.Errorf("Expected %s arguments, got %d", eitherOr, len(args))
}

// expectArgsRange returns an error if the wrong number of arguments was passed.
func expectArgsRange(args []string, limits ...int) error {
	var low int
	high := 100
	switch len(limits) {
	case 0:
		// This indicates a programming error and shouldn’t happen.
		return errors.New("Missing argument constraints")
	case 2:
		high = limits[1]
		fallthrough
	case 1:
		low = limits[0]
	}

	if len(args) < low || len(args) > high && high != 100 {
		if low == 0 {
			if high == 1 {
				return fmt.Errorf("Expected at most 1 argument, got %d", len(args))
			}

			return fmt.Errorf("Expected at most %d arguments, got %d", high, len(args))
		}

		if high == 100 {
			if low == 1 {
				return fmt.Errorf("Expected at least 1 argument, got %d", len(args))
			}

			return fmt.Errorf("Expected at least %d arguments, got %d", low, len(args))
		}

		return fmt.Errorf("Expected between %d and %d arguments, got %d", low, high, len(args))
	}

	return nil
}

// tooManyArgs formats an error with superfluous arguments.
func tooManyArgs(args []string) error {
	if len(args) == 1 {
		return fmt.Errorf("Unexpected additional argument %s", args[0])
	}

	return fmt.Errorf("Unexpected additional arguments %v", args)
}

// processRawDPArgs processes raw device path arguments.
func processRawDPArgs(w *writer, args []string) (uint8, error) {
	err := expectArgs(args, 2)
	if err != nil {
		return 0, err
	}

	subType, err := strconv.ParseUint(args[0], 0, 8)
	if err != nil {
		return 0, err
	}

	return uint8(subType), formatDPArgs(w, args[1:], "*")
}

// decomposeDPInstance decomposes a device path instance into a slice of node names and their
// arguments.
func decomposeDPInstance(s string) ([][]string, error) {
	var out [][]string
	var node []string
	var sb strings.Builder
	inArgs := false
	inQuote := false
	for i := 0; i < len(s); {
		if inQuote {
			if s[i] == '"' {
				inQuote = false
				i++
				continue
			}

			r, _, tl, err := strconv.UnquoteChar(s[i:], '"')
			if err != nil {
				return nil, fmt.Errorf("Invalid quoted string at index %d: %w", i, err)
			}

			sb.WriteRune(r)
			i += len(s[i:]) - len(tl)
			continue
		}

		switch s[i] {
		case '"':
			inQuote = true
		case '(':
			if inArgs {
				return nil, fmt.Errorf("Unexpected '(' at index %d", i)
			}

			node = append(node, strings.ToLower(sb.String()))
			sb.Reset()
			inArgs = true
		case ',':
			if inArgs {
				node = append(node, sb.String())
				sb.Reset()
			} else {
				sb.WriteByte(',')
			}

		case ')':
			if !inArgs {
				return nil, fmt.Errorf("Unexpected ')' at index %d", i)
			}

			if sb.Len() != 0 {
				node = append(node, sb.String())
				sb.Reset()
			}

			inArgs = false
		case '/':
			if inArgs {
				sb.WriteByte('/')
			} else if len(node) == 0 {
				out = append(out, []string{"", sb.String()})
				sb.Reset()
			} else {
				out = append(out, node)
				node = nil
			}

		default:
			sb.WriteByte(s[i])
		}

		i++
	}

	if inQuote {
		return nil, fmt.Errorf("Unterminated quoted string")
	}

	if inArgs {
		return nil, fmt.Errorf("Unterminated argument list")
	}

	if len(node) == 0 {
		return append(out, []string{"", sb.String()}), nil
	}

	return append(out, node), nil
}
