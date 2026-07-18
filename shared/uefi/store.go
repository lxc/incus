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
