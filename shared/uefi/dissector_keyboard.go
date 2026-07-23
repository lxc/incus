package uefi

import (
	"fmt"
	"strings"
	"unicode"
)

// scanCodes is a combination of EFI Scan Codes for `EFI_SIMPLE_TEXT_INPUT_PROTOCOL` and
// `EFI_SIMPLE_TEXT_INPUT_EX_PROTOCOL`.
var scanCodes = map[uint16]string{
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

// keyboard dissects keyboard data.
var keyboard = wrap(func(r *reader) (any, error) {
	keyData, err := r.readU32()
	if err != nil {
		return nil, err
	}

	// We can ignore the CRC.
	_, err = r.read(4)
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

		parts = append(parts, keyText(scanCode, unicodeChar))
	}

	return struct {
		BootOption string `json:"boot_option"`
		Shortcut   string `json:"shortcut"`
	}{
		BootOption: fmt.Sprintf("Boot%04X", bootOption),
		Shortcut:   strings.Join(parts, "+"),
	}, nil
})

// keyText returns a human-friendly representation of an `EFI_INPUT_KEY`.
func keyText(scanCode uint16, unicodeChar uint16) string {
	// Per UEFI semantics, a nonzero scan code represents a special key.
	if scanCode != 0 {
		name, ok := scanCodes[scanCode]
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
