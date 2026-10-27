package uefi

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// scanCodeTexts is a combination of EFI Scan Codes representations for
// `EFI_SIMPLE_TEXT_INPUT_PROTOCOL` and `EFI_SIMPLE_TEXT_INPUT_EX_PROTOCOL`.
var scanCodeTexts = map[uint16]string{
	0x0001: "Up",
	0x0002: "Down",
	0x0003: "Right",
	0x0004: "Left",
	0x0005: "Home",
	0x0006: "End",
	0x0007: "Insert",
	0x0008: "Delete",
	0x0009: "PageUp",
	0x000A: "PageDown",
	0x000B: "F1",
	0x000C: "F2",
	0x000D: "F3",
	0x000E: "F4",
	0x000F: "F5",
	0x0010: "F6",
	0x0011: "F7",
	0x0012: "F8",
	0x0013: "F9",
	0x0014: "F10",
	0x0015: "F11",
	0x0016: "F12",
	0x0017: "Esc",
	0x0048: "Pause",
	0x0068: "F13",
	0x0069: "F14",
	0x006A: "F15",
	0x006B: "F16",
	0x006C: "F17",
	0x006D: "F18",
	0x006E: "F19",
	0x006F: "F20",
	0x0070: "F21",
	0x0071: "F22",
	0x0072: "F23",
	0x0073: "F24",
	0x007F: "Mute",
	0x0080: "VolumeUp",
	0x0081: "VolumeDown",
	0x0100: "BrightnessUp",
	0x0101: "BrightnessDown",
	0x0102: "Suspend",
	0x0103: "Hibernate",
	0x0104: "ToggleDisplay",
	0x0105: "Recovery",
	0x0106: "Eject",
}

// textScanCodes is a combination of EFI Scan Codes interpretations for
// `EFI_SIMPLE_TEXT_INPUT_PROTOCOL` and `EFI_SIMPLE_TEXT_INPUT_EX_PROTOCOL`.
var textScanCodes = map[string]uint16{
	"up":             0x0001,
	"down":           0x0002,
	"right":          0x0003,
	"left":           0x0004,
	"home":           0x0005,
	"end":            0x0006,
	"insert":         0x0007,
	"ins":            0x0007,
	"delete":         0x0008,
	"del":            0x0008,
	"pageup":         0x0009,
	"pgup":           0x0009,
	"pagedown":       0x000A,
	"pgdown":         0x000A,
	"pgdn":           0x000A,
	"f1":             0x000B,
	"f2":             0x000C,
	"f3":             0x000D,
	"f4":             0x000E,
	"f5":             0x000F,
	"f6":             0x0010,
	"f7":             0x0011,
	"f8":             0x0012,
	"f9":             0x0013,
	"f10":            0x0014,
	"f11":            0x0015,
	"f12":            0x0016,
	"esc":            0x0017,
	"escape":         0x0017,
	"pause":          0x0048,
	"f13":            0x0068,
	"f14":            0x0069,
	"f15":            0x006A,
	"f16":            0x006B,
	"f17":            0x006C,
	"f18":            0x006D,
	"f19":            0x006E,
	"f20":            0x006F,
	"f21":            0x0070,
	"f22":            0x0071,
	"f23":            0x0072,
	"f24":            0x0073,
	"mute":           0x007F,
	"volumeup":       0x0080,
	"volup":          0x0080,
	"volumedown":     0x0081,
	"voldown":        0x0081,
	"voldn":          0x0081,
	"brightnessup":   0x0100,
	"briup":          0x0100,
	"brup":           0x0100,
	"brightnessdown": 0x0101,
	"bridown":        0x0101,
	"bridn":          0x0101,
	"brdn":           0x0101,
	"suspend":        0x0102,
	"hibernate":      0x0103,
	"toggledisplay":  0x0104,
	"recovery":       0x0105,
	"eject":          0x0106,
}

type keyboardType struct {
	BootOption string `json:"boot_option"`
	CRC        string `json:"crc"`
	Shortcut   string `json:"shortcut"`
}

// keyboard dissects keyboard data.
var keyboard = wrap(func(r *reader) (*keyboardType, error) {
	keyData, err := r.readU32()
	if err != nil {
		return nil, err
	}

	crc, err := r.readU32()
	if err != nil {
		return nil, err
	}

	bootOption, err := r.readU16()
	if err != nil {
		return nil, err
	}

	count := int((keyData >> 30) & 0x3)

	// The revision field must be 0x00.
	if keyData&0xff != 0 {
		return nil, errUnexpectedData
	}

	parts := []string{}

	// Ctrl.
	if keyData&(1<<9) != 0 {
		parts = append(parts, "Ctrl")
	}

	// Alt.
	if keyData&(1<<10) != 0 {
		parts = append(parts, "Alt")
	}

	// Shift.
	if keyData&(1<<8) != 0 {
		parts = append(parts, "Shift")
	}

	// Meta.
	if keyData&(1<<11) != 0 {
		parts = append(parts, "Meta")
	}

	// Menu.
	if keyData&(1<<12) != 0 {
		parts = append(parts, "Menu")
	}

	// SysRq.
	if keyData&(1<<13) != 0 {
		parts = append(parts, "SysRq")
	}

	for range count {
		scanCode, err := r.readU16()
		if err != nil {
			return nil, err
		}

		unicodeChar, err := r.readU16()
		if err != nil {
			return nil, err
		}

		parts = append(parts, dissectKey(scanCode, unicodeChar))
	}

	return &keyboardType{
		BootOption: fmt.Sprintf("Boot%04X", bootOption),
		CRC:        fmt.Sprintf("0x%08x", crc),
		Shortcut:   strings.Join(parts, "+"),
	}, nil
}, func(w *writer, v *keyboardType) error {
	parts := strings.Split(strings.ToLower(v.Shortcut), "+")
	var keyData uint32
	var rawData []uint16
	count := 0
	for _, part := range parts {
		switch part {
		case "ctrl":
			keyData |= 1 << 9
		case "alt":
			keyData |= 1 << 10
		case "shift":
			keyData |= 1 << 8
		case "meta", "logo", "win":
			keyData |= 1 << 11
		case "menu":
			keyData |= 1 << 12
		case "sysrq":
			keyData |= 1 << 13
		default:
			count++
			if count > 3 {
				return fmt.Errorf("Failed to parse key combination %s: Too many simultaneous keys", v.Shortcut)
			}

			scanCode, unicodeChar, err := formatKey(part)
			if err != nil {
				return fmt.Errorf("Failed to parse key combination %s: %w", v.Shortcut, err)
			}

			rawData = append(rawData, scanCode, unicodeChar)
		}
	}

	keyData |= uint32(count << 30)
	err := w.writeU32(keyData)
	if err != nil {
		return err
	}

	crc, err := strconv.ParseUint(v.CRC, 0, 32)
	if err != nil {
		return err
	}

	err = w.writeU32(uint32(crc))
	if err != nil {
		return err
	}

	bootOption, ok := strings.CutPrefix(strings.ToLower(v.BootOption), "boot")
	if !ok {
		return fmt.Errorf("Failed to parse boot option %s", v.BootOption)
	}

	bootIdx, err := strconv.ParseUint(bootOption, 16, 16)
	if err != nil {
		return fmt.Errorf("Failed to parse boot option %s: %w", v.BootOption, err)
	}

	err = w.writeU16(uint16(bootIdx))
	if err != nil {
		return err
	}

	for _, raw := range rawData {
		err = w.writeU16(raw)
		if err != nil {
			return err
		}
	}

	return nil
})

// dissectKey returns a human-friendly representation of an `EFI_INPUT_KEY`.
func dissectKey(scanCode uint16, unicodeChar uint16) string {
	// Per UEFI semantics, a nonzero scan code represents a special key.
	if scanCode != 0 {
		name, ok := scanCodeTexts[scanCode]
		if ok {
			return name
		}

		if scanCode >= 0x8000 {
			return fmt.Sprintf("OEM(0x%04X)", scanCode)
		}

		return fmt.Sprintf("Scan(0x%04X)", scanCode)
	}

	switch unicodeChar {
	case 0x0000:
		return "Null"
	case 0x0008:
		return "Backspace"
	case 0x0009:
		return "Tab"
	case 0x000A:
		return "LF"
	case 0x000D:
		return "Enter"
	case 0x001B:
		// This key should in theory be covered by a scan code.
		return "Esc"
	case 0x0020:
		return "Space"
	case 0x002B:
		// Avoid ambiguous output such as `Ctrl++`.
		return "Plus"
	}

	r := rune(unicodeChar)
	if !unicode.IsPrint(r) {
		return fmt.Sprintf("Unicode(%04X)", unicodeChar)
	}

	return string(unicode.ToUpper(r))
}

// formatKey returns an `EFI_INPUT_KEY` from its human-friendly representation.
func formatKey(k string) (uint16, uint16, error) {
	scanCode, ok := textScanCodes[k]
	if ok {
		return scanCode, 0x0000, nil
	}

	// Quick and dirty way to parse parenthesized atoms.
	s, ok := strings.CutPrefix(k, ")")
	if ok {
		keyType, data, ok := strings.Cut(s, "(")
		if !ok {
			return 0, 0, fmt.Errorf("Couldn’t parse key %s", k)
		}

		code, err := strconv.ParseUint(data, 0, 16)
		if err != nil {
			return 0, 0, fmt.Errorf("Couldn’t parse key %s: %w", k, err)
		}

		switch strings.ToLower(keyType) {
		case "oem", "scan":
			return uint16(code), 0x0000, nil
		case "unicode":
			return 0x0000, uint16(code), nil
		default:
			return 0, 0, fmt.Errorf("Couldn’t parse key %s: Unknown key type %s", k, keyType)
		}
	}

	switch strings.ToLower(k) {
	case "null", "nul":
		return 0x0000, 0x0000, nil
	case "backspace", "bksp":
		return 0x0000, 0x0008, nil
	case "tab":
		return 0x0000, 0x0009, nil
	case "lf", "linefeed":
		return 0x0000, 0x000a, nil
	case "enter":
		return 0x0000, 0x000d, nil
	case "space", " ":
		return 0x0000, 0x0020, nil
	case "plus":
		return 0x0000, 0x002b, nil
	}

	r := []rune(k)
	if len(r) != 1 || r[0] > 0xffff {
		return 0, 0, fmt.Errorf("Couldn’t parse key %s", k)
	}

	return 0x0000, uint16(r[0]), nil
}
