package uefi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type dissector struct {
	dissect func([]byte) (any, error)
	format  func(json.RawMessage) ([]byte, error)
}

// wrap wraps a variable dissector.
func wrap[T any](f func(*reader) (T, error), gs ...func(*writer, T) error) dissector {
	g := func(*writer, T) error {
		return errNotImplemented
	}

	if len(gs) > 0 {
		g = gs[0]
	}

	return dissector{
		dissect: func(b []byte) (any, error) {
			r := newReader(b)
			v, err := f(r)
			if err != nil {
				return nil, err
			}

			if !r.eof() {
				return nil, errUnexpectedData
			}

			return v, nil
		},
		format: func(in json.RawMessage) ([]byte, error) {
			w := newWriter()
			var v T
			err := json.Unmarshal(in, &v)
			if err != nil {
				return nil, err
			}

			err = g(w, v)
			if err != nil {
				return nil, err
			}

			return w.data, nil
		},
	}
}

// b8 dissects 8-bit booleans.
var b8 = wrap((*reader).readB8, (*writer).writeB8)

// u16 dissects 16-bit integers.
var u16 = wrap((*reader).readU16, (*writer).writeU16)

// u32 dissects 32-bit integers.
var u32 = wrap((*reader).readU32, (*writer).writeU32)

// zn8 dissects NUL-terminated UTF8 strings.
var zn8 = wrap(func(r *reader) (string, error) { return r.readZn8() }, func(w *writer, v string) error { return w.writeZn8(v) })

// z8 dissects UTF8 strings.
var z8 = wrap(func(r *reader) (string, error) { return r.readZ8(r.rem()) }, func(w *writer, v string) error { return w.writeZ8(v) })

// bootOrder dissects `BootOrder` and `DriverOrder` variables.
func bootOrder(prefix string) dissector {
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
	}, func(w *writer, v []string) error {
		for _, entry := range v {
			suffix, ok := strings.CutPrefix(entry, prefix)
			if !ok || len(suffix) != 4 {
				return fmt.Errorf("Entries must be of the form %s####", prefix)
			}

			n, err := strconv.ParseUint(suffix, 16, 16)
			if err != nil {
				return fmt.Errorf("Invalid entry ID: %s", suffix)
			}

			err = w.writeU16(uint16(n))
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// boot dissects `Boot####`, `Driver####`, `SysPrep####`, `OsRecovery####` and
// `PlatformRecovery####` variables.
// TODO: Implement variable formatting.
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

type eslEntry struct {
	Owner string `json:"owner"`
	Data  []byte `json:"data"`
}

type eslNode struct {
	Type    string     `json:"type"`
	Header  []byte     `json:"header,omitempty"`
	Entries []eslEntry `json:"entries"`
}

// esl dissects EFI signature lists.
var esl = wrap(func(r *reader) ([]eslNode, error) {
	db := []eslNode{}
	for !r.eof() {
		start := r.pos()
		sigGUID, err := r.readGUID()
		if err != nil {
			return nil, err
		}

		typeStr, ok := guidSigs[sigGUID]
		if !ok {
			return nil, errUnexpectedData
		}

		listSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		headerSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		sigSize, err := r.readU32()
		if err != nil {
			return nil, err
		}

		header, err := r.read(int(headerSize))
		if err != nil {
			return nil, err
		}

		lst := eslNode{Type: typeStr, Header: header}
		for r.pos()-start < int(listSize) {
			owner, err := r.readGUID()
			if err != nil {
				return nil, err
			}

			body, err := r.read(int(sigSize) - 16)
			if err != nil {
				return nil, err
			}

			lst.Entries = append(lst.Entries, eslEntry{Owner: owner, Data: body})
		}

		db = append(db, lst)
	}

	return db, nil
}, func(w *writer, v []eslNode) error {
	for _, node := range v {
		start := w.size()
		sigGUID, ok := sigGUIDs[node.Type]
		if !ok {
			return fmt.Errorf("Unknown signature type %s", node.Type)
		}

		err := w.writeGUID(sigGUID)
		if err != nil {
			return err
		}

		listSizeIdx := w.size()
		// To be filled later.
		err = w.writeU32(0)
		if err != nil {
			return err
		}

		err = w.writeU32(uint32(len(node.Header)))
		if err != nil {
			return err
		}

		// We guess the signature size based on the first entry.
		sigSize := uint32(0)
		if len(node.Entries) > 0 {
			sigSize = uint32(16 + len(node.Entries[0].Data))
		}

		err = w.writeU32(sigSize)
		if err != nil {
			return err
		}

		err = w.write(node.Header)
		if err != nil {
			return err
		}

		for _, entry := range node.Entries {
			err = w.writeGUID(entry.Owner)
			if err != nil {
				return err
			}

			if len(entry.Data) != int(sigSize)-16 {
				return errors.New("Inhomogeneous signature sizes")
			}

			err = w.write(entry.Data)
			if err != nil {
				return err
			}
		}

		err = w.writeU32At(uint32(w.size()-start), listSizeIdx)
		if err != nil {
			return err
		}
	}

	return nil
})

type errorFlagType struct {
	UserError   bool `json:"user_error"`
	SystemError bool `json:"system_error"`
}

// errorFlag dissects `VarErrorFlag` variables.
var errorFlag = wrap(func(r *reader) (*errorFlagType, error) {
	v, err := r.readU8()
	if err != nil {
		return nil, err
	}

	if v&0xEE != 0xEE {
		return nil, errUnexpectedData
	}

	return &errorFlagType{
		UserError:   v&0x01 == 0,
		SystemError: v&0x10 == 0,
	}, nil
}, func(w *writer, v *errorFlagType) error {
	b := uint8(0xFF)
	if v.UserError {
		b &^= 0x01
	}

	if v.SystemError {
		b &^= 0x10
	}

	return w.writeU8(b)
})

// attemptOrder dissects `InitialAttemptOrder` variables.
var attemptOrder = wrap(func(r *reader) ([]string, error) {
	resp := []string{}
	for !r.eof() {
		v, err := r.readU8()
		if err != nil {
			return nil, err
		}

		resp = append(resp, fmt.Sprintf("Attempt %d", v))
	}

	return resp, nil
}, func(w *writer, v []string) error {
	for _, entry := range v {
		suffix, ok := strings.CutPrefix(entry, "Attempt ")
		if !ok {
			return errors.New("Entries must start with “Attempt ”")
		}

		n, err := strconv.ParseUint(suffix, 10, 8)
		if err != nil {
			return fmt.Errorf("Invalid entry ID: %s", suffix)
		}

		err = w.writeU8(uint8(n))
		if err != nil {
			return err
		}
	}

	return nil
})

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
}, func(w *writer, v string) error {
	var b uint8
	switch v {
	case "none", "0":
		b = 0
	case "1.2", "1":
		b = 1
	case "2.0", "2":
		b = 2
	default:
		return fmt.Errorf("Unexpected version %s, expected one of none, 1.2, or 2.0", v)
	}

	return w.writeU8(b)
})

type tcg2VersionType struct {
	PPIVersion        string `json:"ppi_version"`
	ACPITableRevision uint8  `json:"acpi_table_revision"`
}

// tcg2Version dissects `TCG2_VERSION` variables.
var tcg2Version = wrap(func(r *reader) (*tcg2VersionType, error) {
	ppiVersion, err := r.readZn8(8)
	if err != nil {
		return nil, err
	}

	acpiTableRevision, err := r.readU8()
	if err != nil {
		return nil, err
	}

	// Padding
	err = r.skip(7)
	if err != nil {
		return nil, err
	}

	return &tcg2VersionType{
		PPIVersion:        ppiVersion,
		ACPITableRevision: acpiTableRevision,
	}, nil
}, func(w *writer, v *tcg2VersionType) error {
	err := w.writeZn8(v.PPIVersion, 8)
	if err != nil {
		return err
	}

	err = w.writeU8(v.ACPITableRevision)
	if err != nil {
		return err
	}

	// Padding
	err = w.skip(7)
	if err != nil {
		return err
	}

	return nil
})

type osIndicationsType struct {
	BootToFWUI                   bool   `json:"boot_to_fw_ui"`
	TimestampRevocation          bool   `json:"timestamp_revocation"`
	FileCapsuleDeliverySupported bool   `json:"file_capsule_delivery_supported"`
	FMPCapsuleSupported          bool   `json:"fmp_capsule_supported"`
	CapsuleResultVarSupported    bool   `json:"capsule_result_var_supported"`
	StartOSRecovery              bool   `json:"start_os_recovery"`
	StartPlatformRecovery        bool   `json:"start_platform_recovery"`
	JSONConfigDataRefresh        bool   `json:"json_config_data_refresh"`
	Reserved                     uint64 `json:"reserved,omitempty"`
}

// osIndications dissects `OsIndications` variables.
var osIndications = wrap(func(r *reader) (*osIndicationsType, error) {
	v, err := r.readU64()
	if err != nil {
		return nil, err
	}

	return &osIndicationsType{
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
}, func(w *writer, v *osIndicationsType) error {
	var b uint64
	if v.Reserved&0x0000_0000_0000_00FF > 0 {
		return errors.New("The last 8 bits of the reserved field must be unset")
	}

	if v.BootToFWUI {
		b |= 0x0000_0000_0000_0001
	}

	if v.TimestampRevocation {
		b |= 0x0000_0000_0000_0002
	}

	if v.FileCapsuleDeliverySupported {
		b |= 0x0000_0000_0000_0004
	}

	if v.FMPCapsuleSupported {
		b |= 0x0000_0000_0000_0008
	}

	if v.CapsuleResultVarSupported {
		b |= 0x0000_0000_0000_0010
	}

	if v.StartOSRecovery {
		b |= 0x0000_0000_0000_0020
	}

	if v.StartPlatformRecovery {
		b |= 0x0000_0000_0000_0040
	}

	if v.JSONConfigDataRefresh {
		b |= 0x0000_0000_0000_0080
	}

	b |= v.Reserved
	return w.writeU64(b)
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
}, func(w *writer, v string) error {
	var b uint8
	switch v {
	case "unlocked", "0":
		b = 0
	case "locked_without_key", "1":
		b = 1
	case "locked_with_key", "2":
		b = 2
	default:
		return fmt.Errorf("Unexpected lock value %s, expected one of unlock, locked_without_key, or locked_with_key", v)
	}

	return w.writeU8(b)
})

type morControlType struct {
	ClearMemory       bool  `json:"clear_memory"`
	DisableAutoDetect bool  `json:"disable_autodetect"`
	Reserved          uint8 `json:"reserved,omitempty"`
}

// morControl dissects `MemoryOverwriteRequestControl` variables.
var morControl = wrap(func(r *reader) (*morControlType, error) {
	v, err := r.readU8()
	if err != nil {
		return nil, err
	}

	return &morControlType{
		ClearMemory:       v&0x01 != 0,
		DisableAutoDetect: v&0x10 != 0,
		Reserved:          v & 0xEE,
	}, nil
}, func(w *writer, v *morControlType) error {
	var b uint8
	if v.Reserved&0x11 > 0 {
		return errors.New("Bits 0x01 and 0x10 of the reserved field must be unset")
	}

	if v.ClearMemory {
		b |= 0x01
	}

	if v.DisableAutoDetect {
		b |= 0x10
	}

	b |= v.Reserved
	return w.writeU8(b)
})

type certDBEntry struct {
	Name   string `json:"name"`
	GUID   string `json:"guid"`
	Digest []byte `json:"digest"`
}

// certDB dissects `certdb` variables.
var certDB = wrap(func(r *reader) ([]certDBEntry, error) {
	size, err := r.readU32()
	if err != nil {
		return nil, err
	}

	if size != uint32(len(r.data)) {
		return nil, errUnexpectedData
	}

	db := []certDBEntry{}
	for !r.eof() {
		start := r.pos()
		guid, err := r.readGUID()
		if err != nil {
			return nil, err
		}

		nodeSize, err := r.readU32()
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

		if nodeSize != uint32(r.pos()-start) {
			return nil, errUnexpectedData
		}

		db = append(db, certDBEntry{Name: name, GUID: guid, Digest: digest})
	}

	return db, nil
}, func(w *writer, v []certDBEntry) error {
	// To be filled later.
	err := w.writeU32(0)
	if err != nil {
		return err
	}

	for _, entry := range v {
		err = w.writeGUID(entry.GUID)
		if err != nil {
			return err
		}

		err = w.writeU32(uint32(28 + 2*len(entry.Name) + len(entry.Digest)))
		if err != nil {
			return err
		}

		err = w.writeU32(uint32(len(entry.Name)))
		if err != nil {
			return err
		}

		err = w.writeU32(uint32(len(entry.Digest)))
		if err != nil {
			return err
		}

		err = w.writeZ16(entry.Name)
		if err != nil {
			return err
		}

		err = w.write(entry.Digest)
		if err != nil {
			return err
		}
	}

	return w.writeU32At(uint32(w.size()), 0)
})
