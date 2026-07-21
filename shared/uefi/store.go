package uefi

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/lxc/incus/v7/shared/api"
)

type blockMapEntry struct {
	Count uint32 `json:"count"`
	Size  uint32 `json:"size"`
}

// Store is a projection of the on-disk OVMF variable store format.
type Store struct {
	Vars     map[string]map[string]*api.InstanceNVRAMVariable
	attrs    uint32
	blockMap []blockMapEntry
	length   uint64
	varSize  uint32
}

// ParseNVRAM parses the contents of an OVMF NVRAM store.
func ParseNVRAM(data []byte) (*Store, error) {
	r := newReader(data)

	zeroVector, err := r.read(16)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(zeroVector, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) {
		return nil, fmt.Errorf("Invalid zero vector: %x", zeroVector)
	}

	fsguid, err := r.readGUID()
	if err != nil {
		return nil, err
	}

	if fsguid != EfiSystemNvDataFvGuid {
		return nil, fmt.Errorf("Invalid GUID: %s", fsguid)
	}

	length, err := r.readU64()
	if err != nil {
		return nil, err
	}

	if length > uint64(len(data)) {
		return nil, fmt.Errorf("Invalid length: %d", length)
	}

	sig, err := r.readZ8(4)
	if err != nil {
		return nil, err
	}

	if sig != "_FVH" {
		return nil, fmt.Errorf("Invalid FVH signature: %s", sig)
	}

	attrs, err := r.readU32()
	if err != nil {
		return nil, err
	}

	hlength, err := r.readU16()
	if err != nil {
		return nil, err
	}

	csumHdr, err := r.readU16()
	if err != nil {
		return nil, err
	}

	if int(hlength) > len(data) {
		return nil, fmt.Errorf("Invalid header length: %d", hlength)
	}

	if csum16(data[:hlength]) != 0 {
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
		return nil, fmt.Errorf("Wrong value for FVH.Reserved: 0x%x", reserved)
	}

	rev, err := r.readU8()
	if err != nil {
		return nil, err
	}

	if rev != 2 {
		return nil, fmt.Errorf("Invalid FVH Revision: 0x%x", rev)
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

		blockMap = append(blockMap, blockMapEntry{Count: blockCnt, Size: blockBytes})
		totalBytes += uint64(blockCnt) * uint64(blockBytes)
	}

	if totalBytes != length {
		return nil, fmt.Errorf("Invalid blockmap: %v", blockMap)
	}

	if r.pos() != int(hlength) {
		return nil, fmt.Errorf("Invalid header length: %d", hlength)
	}

	vsGUID, err := r.readGUID()
	if err != nil {
		return nil, err
	}

	if vsGUID != EfiAuthenticatedVariableGuid {
		return nil, fmt.Errorf("Invalid Varstore GUID: %s", vsGUID)
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
		return nil, fmt.Errorf("Invalid Varstore Status: %x", status)
	}

	s := &Store{attrs: attrs, blockMap: blockMap, length: length, varSize: varSize, Vars: make(map[string]map[string]*api.InstanceNVRAMVariable)}
	for {
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
			v := api.InstanceNVRAMVariable{Attributes: parseAttributes(rawAttributes), Binary: data, Timestamp: timestamp}
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

	err = w.writeU64(s.length)
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
		err = w.writeU32(b.Count)
		if err != nil {
			return nil, err
		}

		err = w.writeU32(b.Size)
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

	err = w.writeU16At(uint16(w.size()), hlenPos)
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

			err = w.writeU32(dumpAttributes(v.Attributes))
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

	if uint64(w.size()) > s.length {
		return nil, fmt.Errorf("Variables require %d bytes but store length is %d", w.size(), s.length)
	}

	for uint64(w.size()) < s.length {
		err = w.writeU8(0xff)
		if err != nil {
			return nil, err
		}
	}

	return w.data, nil
}
