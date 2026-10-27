package uefi

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/lxc/incus/v7/shared/api"
)

type blockMapEntry struct {
	count uint32
	size  uint32
}

// Store is a projection of the on-disk OVMF variable store format. The structure DOES NOT handle
// concurrent access.
type Store struct {
	Vars     map[string]map[string]*api.InstanceNVRAMVariable
	attrs    uint32
	blockMap []blockMapEntry
	fvLength uint64
	varSize  uint32
	fileSize int
	rest     []byte
	modified bool
}

// ParseNVRAM parses the contents of an OVMF NVRAM store.
func ParseNVRAM(data []byte) (*Store, error) {
	r := newReader(data)

	zeroVector, err := r.read(16)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(zeroVector, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) {
		return nil, fmt.Errorf("Invalid zero vector; got %x", zeroVector)
	}

	fsguid, err := r.readGUID()
	if err != nil {
		return nil, err
	}

	if fsguid != EfiSystemNvDataFvGuid {
		return nil, fmt.Errorf("Invalid GUID; expected %s, got %s", EfiSystemNvDataFvGuid, fsguid)
	}

	fvLength, err := r.readU64()
	if err != nil {
		return nil, err
	}

	if fvLength > uint64(len(data)) {
		return nil, fmt.Errorf("Invalid firmware volume length; %d extends past file size %d", fvLength, len(data))
	}

	sig, err := r.readZ8(4)
	if err != nil {
		return nil, err
	}

	if sig != "_FVH" {
		return nil, fmt.Errorf("Invalid signature; expected _FVH, got %s", sig)
	}

	attrs, err := r.readU32()
	if err != nil {
		return nil, err
	}

	headerLength, err := r.readU16()
	if err != nil {
		return nil, err
	}

	if uint64(headerLength) > fvLength {
		return nil, fmt.Errorf("Invalid header length; %d extends past volume size %d", headerLength, fvLength)
	}

	csumHdr, err := r.readU16()
	if err != nil {
		return nil, err
	}

	if csum16(data[:headerLength]) != 0 {
		return nil, fmt.Errorf("Invalid header checksum: %x", csumHdr)
	}

	extHdrOffset, err := r.readU16()
	if err != nil {
		return nil, err
	}

	if extHdrOffset != 0 {
		return nil, errors.New("FVH with extension header not supported")
	}

	reserved, err := r.readU8()
	if err != nil {
		return nil, err
	}

	if reserved != 0 {
		return nil, fmt.Errorf("Invalid reserved field; expected 0x0, got 0x%x", reserved)
	}

	rev, err := r.readU8()
	if err != nil {
		return nil, err
	}

	if rev != 2 {
		return nil, fmt.Errorf("Invalid revision; expected 0x2, got 0x%x", rev)
	}

	var blockMap []blockMapEntry
	var totalBytes uint64
	for {
		blockCnt, err := r.readU32()
		if err != nil {
			return nil, err
		}

		blockBytes, err := r.readU32()
		if err != nil {
			return nil, err
		}

		if blockCnt == 0 && blockBytes == 0 {
			break
		}

		blockMap = append(blockMap, blockMapEntry{count: blockCnt, size: blockBytes})
		totalBytes += uint64(blockCnt) * uint64(blockBytes)
	}

	if totalBytes != fvLength {
		return nil, fmt.Errorf("Invalid blockmap %v", blockMap)
	}

	if r.pos() != int(headerLength) {
		return nil, fmt.Errorf("Invalid header length; expected %d, got %d", headerLength, r.pos())
	}

	vsGUID, err := r.readGUID()
	if err != nil {
		return nil, err
	}

	if vsGUID != EfiAuthenticatedVariableGuid {
		return nil, fmt.Errorf("Invalid store GUID; expected %s, got %s", EfiAuthenticatedVariableGuid, vsGUID)
	}

	varSize, err := r.readU32()
	if err != nil {
		return nil, err
	}

	status, err := r.read(8)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(status, []byte{0x5a, 0xfe, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}) {
		return nil, fmt.Errorf("Invalid store status; expected 0x5afe000000000000 0x%x", status)
	}

	varStoreLength := int(varSize) + int(headerLength)
	s := &Store{attrs: attrs, blockMap: blockMap, fvLength: fvLength, varSize: varSize, fileSize: len(data), Vars: make(map[string]map[string]*api.InstanceNVRAMVariable)}
	for r.pos() < varStoreLength {
		start, err := r.readU16()
		if err != nil {
			return nil, err
		}

		if start != 0x55aa {
			break
		}

		state, err := r.readU8()
		if err != nil {
			return nil, err
		}

		err = r.skip(1)
		if err != nil {
			return nil, err
		}

		rawAttributes, err := r.readU32()
		if err != nil {
			return nil, err
		}

		err = r.skip(8)
		if err != nil {
			return nil, err
		}

		timestamp, err := r.readTimestamp()
		if err != nil {
			return nil, err
		}

		err = r.skip(4)
		if err != nil {
			return nil, err
		}

		nameLen, err := r.readU32()
		if err != nil {
			return nil, err
		}

		dataLen, err := r.readU32()
		if err != nil {
			return nil, err
		}

		guid, err := r.readGUID()
		if err != nil {
			return nil, err
		}

		name, err := r.readZn16(int(nameLen) / 2)
		if err != nil {
			return nil, err
		}

		data, err := r.read(int(dataLen))
		if err != nil {
			return nil, err
		}

		if state == 0x3f {
			varPut := api.InstanceNVRAMVariablePut{Attributes: ParseAttributes(rawAttributes), Timestamp: timestamp}
			v := api.InstanceNVRAMVariable{InstanceNVRAMVariablePut: varPut, Binary: data}
			_, ok := s.Vars[guid]
			if !ok {
				s.Vars[guid] = make(map[string]*api.InstanceNVRAMVariable)
			}

			s.Vars[guid][name] = &v
		}

		err = r.seek((r.pos() + 0x3) &^ 0x3)
		if err != nil {
			return nil, err
		}
	}

	if r.pos() > varStoreLength {
		return nil, fmt.Errorf("Read variable past the variable store")
	}

	err = r.seek(varStoreLength)
	if err != nil {
		return nil, err
	}

	rest, err := r.read(int(fvLength) - varStoreLength)
	if err != nil {
		return nil, err
	}

	s.rest = rest
	return s, nil
}

// Bytes writes a binary OVMF NVRAM store.
func (s *Store) Bytes() ([]byte, error) {
	w := newWriter()
	err := w.write([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	err = w.writeGUID(EfiSystemNvDataFvGuid)
	if err != nil {
		return nil, err
	}

	err = w.writeU64(s.fvLength)
	if err != nil {
		return nil, err
	}

	err = w.writeZ8("_FVH")
	if err != nil {
		return nil, err
	}

	err = w.writeU32(s.attrs)
	if err != nil {
		return nil, err
	}

	hlenPos := w.size()
	err = w.writeU16(0) // Header length to fill later.
	if err != nil {
		return nil, err
	}

	err = w.writeU16(0) // Header checksum to fill later.
	if err != nil {
		return nil, err
	}

	err = w.writeU16(0) // Extension header.
	if err != nil {
		return nil, err
	}

	err = w.writeU8(0) // Reserved.
	if err != nil {
		return nil, err
	}

	err = w.writeU8(2) // Revision.
	if err != nil {
		return nil, err
	}

	for _, b := range s.blockMap {
		err = w.writeU32(b.count)
		if err != nil {
			return nil, err
		}

		err = w.writeU32(b.size)
		if err != nil {
			return nil, err
		}
	}

	err = w.writeU32(0)
	if err != nil {
		return nil, err
	}

	err = w.writeU32(0)
	if err != nil {
		return nil, err
	}

	headerLength := w.size()
	err = w.writeU16At(uint16(headerLength), hlenPos)
	if err != nil {
		return nil, err
	}

	err = w.writeU16At(-csum16(w.data), hlenPos+2)
	if err != nil {
		return nil, err
	}

	err = w.writeGUID(EfiAuthenticatedVariableGuid)
	if err != nil {
		return nil, err
	}

	err = w.writeU32(s.varSize)
	if err != nil {
		return nil, err
	}

	err = w.write([]byte{0x5a, 0xfe, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if err != nil {
		return nil, err
	}

	varStoreLength := int(s.varSize) + headerLength
	if len(s.rest) != int(s.fvLength)-varStoreLength {
		return nil, errors.New("Invalid volume length")
	}

	for guid, vars := range s.Vars {
		for name, v := range vars {
			err = w.writeU16(0x55aa)
			if err != nil {
				return nil, err
			}

			err = w.writeU8(0x3f)
			if err != nil {
				return nil, err
			}

			err = w.writeU8(0)
			if err != nil {
				return nil, err
			}

			err = w.writeU32(DumpAttributes(v.Attributes))
			if err != nil {
				return nil, err
			}

			err = w.writeU64(0)
			if err != nil {
				return nil, err
			}

			err = w.writeTimestamp(v.Timestamp)
			if err != nil {
				return nil, err
			}

			err = w.writeU32(0)
			if err != nil {
				return nil, err
			}

			err = w.writeU32(uint32(len(name)*2 + 2))
			if err != nil {
				return nil, err
			}

			err = w.writeU32(uint32(len(v.Binary)))
			if err != nil {
				return nil, err
			}

			err = w.writeGUID(guid)
			if err != nil {
				return nil, err
			}

			err = w.writeZn16(name)
			if err != nil {
				return nil, err
			}

			err = w.write(v.Binary)
			if err != nil {
				return nil, err
			}

			for w.size()%4 != 0 {
				err = w.writeU8(0)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if w.size() > varStoreLength {
		return nil, fmt.Errorf("Variables require %d bytes but store length is %d", w.size(), varStoreLength)
	}

	for w.size() < varStoreLength {
		err = w.writeU8(0xff)
		if err != nil {
			return nil, err
		}
	}

	err = w.write(s.rest)
	if err != nil {
		return nil, err
	}

	err = w.skip(s.fileSize - int(s.fvLength))
	if err != nil {
		return nil, err
	}

	return w.data, nil
}

// Get gets a variable from the store.
func (s *Store) Get(guid string, varName string) (*api.InstanceNVRAMVariable, bool) {
	vars, ok := s.Vars[guid]
	if !ok {
		return nil, false
	}

	v, ok := vars[varName]
	if !ok {
		return nil, false
	}

	return v, true
}

// Has checks whether the store contains a variable.
func (s *Store) Has(guid string, varName string) bool {
	_, ok := s.Get(guid, varName)
	return ok
}

// Set sets a variable in the store.
func (s *Store) Set(guid string, varName string, v api.InstanceNVRAMVariable) error {
	if !slices.Contains(v.Attributes, "NON_VOLATILE") {
		return errors.New("Volatile UEFI variables cannot be stored in the NVRAM")
	}

	if v.Binary == nil {
		err := Format(&v, guid, varName)
		if err != nil {
			return err
		}
	}

	vars, ok := s.Vars[guid]
	if !ok {
		s.Vars[guid] = map[string]*api.InstanceNVRAMVariable{varName: &v}
		s.modified = true
		return nil
	}

	if s.Vars[guid][varName] == nil || !bytes.Equal(s.Vars[guid][varName].Binary, v.Binary) {
		vars[varName] = &v
		s.modified = true
	}

	return nil
}

// Unset removes a variable from the store.
func (s *Store) Unset(guid string, varName string) bool {
	if s.Has(guid, varName) {
		delete(s.Vars[guid], varName)
		s.modified = true
		return true
	}

	return false
}

// Modified returns whether the store was modified.
func (s *Store) Modified() bool {
	return s.modified
}
