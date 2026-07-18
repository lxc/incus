package uefi

import (
	"encoding/base64"
	"fmt"
)

// WrapDP wraps a variable dissector.
func wrap[T any](f func(*reader) (T, error)) func([]byte) (any, error) {
	return func(b []byte) (any, error) {
		r := newReader(b)
		v, err := f(r)
		if err != nil {
			return nil, err
		}

		if !r.eof() {
			return nil, errUnexpectedData
		}

		return v, nil
	}
}

// b8 dissects 8-bit booleans.
var b8 = wrap((*reader).readB8)

// u16 dissects 16-bit integers.
var u16 = wrap((*reader).readU16)

// u32 dissects 32-bit integers.
var u32 = wrap((*reader).readU32)

// zn8 dissects NUL-terminated UTF8 strings.
var zn8 = wrap(func(r *reader) (string, error) { return r.readZn8() })

// z8 dissects UTF8 strings.
var z8 = wrap(func(r *reader) (string, error) { return r.readZ8(r.rem()) })

// bootOrder dissects `BootOrder` and `DriverOrder` variables.
func bootOrder(prefix string, b []byte) (any, error) {
	return wrap(func(r *reader) ([]string, error) {
		rem := r.rem()
		if rem%2 != 0 {
			return nil, errUnexpectedData
		}

		entries := make([]string, rem/2)
		for i := range entries {
			n, err := r.readU16()
			if err != nil {
				return nil, err
			}

			entries[i] = fmt.Sprintf("%s%04X", prefix, n)
		}

		return entries, nil
	})(b)
}

// boot dissects `Boot####`, `Driver####`, `SysPrep####`, `OsRecovery####` and
// `PlatformRecovery####` variables.
var boot = wrap(func(r *reader) (any, error) {
	attrs, err := r.readU32()
	if err != nil {
		return nil, err
	}

	length, err := r.readU16()
	if err != nil {
		return nil, err
	}

	description, err := r.readZn16()
	if err != nil {
		return nil, err
	}

	b, err := r.read(int(length))
	if err != nil {
		return nil, err
	}

	paths, err := devicePaths(b)
	if err != nil {
		return nil, err
	}

	category := ""
	cat := attrs & 0x1f00
	switch cat {
	case 0:
		category = "boot"
	case 0x100:
		category = "app"
	default:
		category = fmt.Sprintf("0x%x", cat)
	}

	remaining, err := r.read(r.rem())
	if err != nil {
		return nil, err
	}

	return struct {
		Active         bool       `json:"active"`
		ForceReconnect bool       `json:"force_reconnect"`
		Hidden         bool       `json:"hidden"`
		Category       string     `json:"category"`
		Description    string     `json:"description"`
		DevicePaths    [][]string `json:"paths"`
		OptionalData   string     `json:"optional_data,omitempty"`
	}{
		Active:         attrs&0x01 != 0,
		ForceReconnect: attrs&0x02 != 0,
		Hidden:         attrs&0x08 != 0,
		Category:       category,
		Description:    description,
		DevicePaths:    paths,
		OptionalData:   base64.StdEncoding.EncodeToString(remaining),
	}, nil
})

// esl dissects EFI signature lists.
func esl(data []byte) (any, error) {
	type sigData struct {
		Owner string `json:"owner"`
		Data  []byte `json:"data"`
	}

	type sigList struct {
		Type    string    `json:"type"`
		Header  []byte    `json:"header,omitempty"`
		Entries []sigData `json:"entries"`
	}

	var db []sigList
	r := newReader(data)
	for !r.eof() {
		start := r.pos()
		if start > len(data)-28 {
			return nil, errUnexpectedData
		}

		sigType, err := r.readGUID()
		if err != nil {
			return nil, err
		}

		typeStr, err := parseSigType(sigType)
		if err != nil {
			return nil, err
		}

		listSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		end := start + int(listSize)
		if end > len(data) {
			return nil, errUnexpectedData
		}

		headerSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		if listSize < 28+headerSize {
			return nil, errUnexpectedData
		}

		sigSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		if sigSize < 16 {
			return nil, errUnexpectedData
		}

		header, err := r.read(int(headerSize))
		if err != nil {
			return nil, err
		}

		sigBytes := end - r.pos()
		if sigBytes%int(sigSize) != 0 {
			return nil, errUnexpectedData
		}

		lst := sigList{Type: typeStr, Header: header}
		for r.pos() < end {
			owner, err := r.readGUID()
			if err != nil {
				return nil, err
			}

			body, err := r.read(int(sigSize) - 16)
			if err != nil {
				return nil, err
			}

			lst.Entries = append(lst.Entries, sigData{Owner: owner, Data: body})
		}

		db = append(db, lst)
	}

	return db, nil
}

// errorFlag dissects `VarErrorFlag` variables.
var errorFlag = wrap(func(r *reader) (any, error) {
	v, err := r.readU8()
	if err != nil {
		return nil, err
	}

	if v&0xEE != 0xEE {
		return nil, errUnexpectedData
	}

	return struct {
		UserError   bool `json:"user_error"`
		SystemError bool `json:"system_error"`
	}{
		UserError:   v&0x01 == 0,
		SystemError: v&0x10 == 0,
	}, nil
})

// attemptOrder dissects `InitialAttemptOrder` variables.
func attemptOrder(b []byte) ([]string, error) {
	r := newReader(b)
	resp := make([]string, len(b))
	for i := 0; !r.eof(); i += 1 {
		v, err := r.readU8()
		if err != nil {
			return nil, err
		}

		resp[i] = fmt.Sprintf("Attempt %d", v)
	}

	return resp, nil
}

// tpmVersion dissects `TCG2_CONFIGURATION` and `TCG2_DEVICE_DETECTION` variables.
var tpmVersion = wrap(func(r *reader) (string, error) {
	v, err := r.readU8()
	if err != nil {
		return "", err
	}

	switch v {
	case 0:
		return "none", nil
	case 1:
		return "1.2", nil
	case 2:
		return "2.0", nil
	default:
		return "", errUnexpectedData
	}
})

// tcg2Version dissects `TCG2_VERSION` variables.
var tcg2Version = wrap(func(r *reader) (any, error) {
	ppiVersion, err := r.readZn8(8)
	if err != nil {
		return nil, err
	}

	acpiTableRevision, err := r.readU8()
	if err != nil {
		return nil, err
	}

	// Padding
	_, err = r.read(7)
	if err != nil {
		return nil, err
	}

	return struct {
		PPIVersion        string `json:"ppi_version"`
		ACPITableRevision uint8  `json:"acpi_table_revision"`
	}{
		PPIVersion:        ppiVersion,
		ACPITableRevision: acpiTableRevision,
	}, nil
})

// osIndications dissects `OsIndications` variables.
var osIndications = wrap(func(r *reader) (any, error) {
	v, err := r.readU64()
	if err != nil {
		return nil, err
	}

	return struct {
		BootToFWUI                   bool   `json:"boot_to_fw_ui"`
		TimestampRevocation          bool   `json:"timestamp_revocation"`
		FileCapsuleDeliverySupported bool   `json:"file_capsule_delivery_supported"`
		FMPCapsuleSupported          bool   `json:"fmp_capsule_supported"`
		CapsuleResultVarSupported    bool   `json:"capsule_result_var_supported"`
		StartOSRecovery              bool   `json:"start_os_recovery"`
		StartPlatformRecovery        bool   `json:"start_platform_recovery"`
		JSONConfigDataRefresh        bool   `json:"json_config_data_refresh"`
		Reserved                     uint64 `json:"reserved,omitempty"`
	}{
		BootToFWUI:                   v&0x0000_0000_0000_0001 != 0,
		TimestampRevocation:          v&0x0000_0000_0000_0002 != 0,
		FileCapsuleDeliverySupported: v&0x0000_0000_0000_0004 != 0,
		FMPCapsuleSupported:          v&0x0000_0000_0000_0008 != 0,
		CapsuleResultVarSupported:    v&0x0000_0000_0000_0010 != 0,
		StartOSRecovery:              v&0x0000_0000_0000_0020 != 0,
		StartPlatformRecovery:        v&0x0000_0000_0000_0040 != 0,
		JSONConfigDataRefresh:        v&0x0000_0000_0000_0080 != 0,
		Reserved:                     v & 0xFFFF_FFFF_FFFF_FF00,
	}, nil
})

// morControlLock dissects `MemoryOverwriteRequestControlLock` variables.
var morControlLock = wrap(func(r *reader) (string, error) {
	v, err := r.readU8()
	if err != nil {
		return "", err
	}

	switch v {
	case 0:
		return "unlocked", nil
	case 1:
		return "locked_without_key", nil
	case 2:
		return "locked_with_key", nil
	default:
		return "", errUnexpectedData
	}
})

// morControl dissects `MemoryOverwriteRequestControl` variables.
var morControl = wrap(func(r *reader) (any, error) {
	v, err := r.readU8()
	if err != nil {
		return "", err
	}

	return struct {
		ClearMemory       bool  `json:"clear_memory"`
		DisableAutoDetect bool  `json:"disable_autodetect"`
		Reserved          uint8 `json:"reserved,omitempty"`
	}{
		ClearMemory:       v&0x01 != 0,
		DisableAutoDetect: v&0x10 != 0,
		Reserved:          v & 0xEE,
	}, nil
})

// certDB dissects `certdb` variables.
var certDB = wrap(func(r *reader) (any, error) {
	type certDBEntry struct {
		Name   string
		GUID   string
		Digest []byte
	}

	size, err := r.readU32()
	if err != nil {
		return nil, err
	}

	if size != uint32(len(r.data)) {
		return nil, errUnexpectedData
	}

	var db []certDBEntry
	for !r.eof() {
		guid, err := r.readGUID()
		if err != nil {
			return nil, err
		}

		err = r.skip(4)
		if err != nil {
			return nil, err
		}

		nameSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		digestSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		name, err := r.readZ16(int(nameSize))
		if err != nil {
			return nil, err
		}

		digest, err := r.read(int(digestSize))
		if err != nil {
			return nil, err
		}

		db = append(db, certDBEntry{Name: name, GUID: guid, Digest: digest})
	}

	return db, nil
})
