package local

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// xlMetaMagic is the four-byte prefix of every xl.meta file written by minio.
var xlMetaMagic = []byte{'X', 'L', '2', ' '}

// xlInlineData parses a minio xl.meta file and returns the inline data blob
// for the object, if the object is stored inline.
//
// The file layout is:
//
//	bytes 0-3:  "XL2 "
//	bytes 4-7:  "1   " for format 1.0, otherwise little-endian major and minor
//	then:       format 1.0 stores the metadata unwrapped and has no inline data
//	            format 1.1+ wraps the metadata in a msgpack bin
//	            format 1.2+ follows the metadata with a msgpack uint32 CRC
//	trailer:    inline data, if any: 0x01 then a msgpack map[string][]byte
//
// Returns (nil, nil) if the file is well-formed but contains no inline
// data (e.g. the object's data is stored externally as part files).
func xlInlineData(b []byte) ([]byte, error) {
	if len(b) <= 8 {
		return nil, errors.New("xl.meta truncated")
	}

	if !bytes.Equal(b[:4], xlMetaMagic) {
		return nil, errors.New("xl.meta bad magic")
	}

	var major, minor uint16
	if bytes.Equal(b[4:8], []byte("1   ")) {
		major, minor = 1, 0
	} else {
		major = binary.LittleEndian.Uint16(b[4:6])
		minor = binary.LittleEndian.Uint16(b[6:8])
	}

	if major != 1 {
		return nil, fmt.Errorf("xl.meta unsupported format version %d.%d", major, minor)
	}

	if minor == 0 {
		return nil, nil
	}

	r := newMsgpReader(b[8:])

	_, err := r.readBin()
	if err != nil {
		return nil, fmt.Errorf("xl.meta metadata: %w", err)
	}

	if minor >= 2 {
		_, err = r.readUint32()
		if err != nil {
			return nil, fmt.Errorf("xl.meta checksum: %w", err)
		}
	}

	inline := r.rest()
	if len(inline) == 0 {
		return nil, nil
	}

	if inline[0] != 0x01 {
		return nil, fmt.Errorf("xl.meta unsupported inline data version %d", inline[0])
	}

	r = newMsgpReader(inline[1:])

	count, err := r.readMapLen()
	if err != nil {
		return nil, fmt.Errorf("xl.meta inline data: %w", err)
	}

	if count == 0 {
		return nil, nil
	}

	_, err = r.readStr()
	if err != nil {
		return nil, fmt.Errorf("xl.meta inline data: %w", err)
	}

	data, err := r.readBin()
	if err != nil {
		return nil, fmt.Errorf("xl.meta inline data: %w", err)
	}

	return data, nil
}

// msgpReader is a minimal msgpack reader supporting only the type families
// used by minio xl.meta: bin, str, uint32, fixmap, map16/32.
type msgpReader struct {
	b []byte
	i int
}

func newMsgpReader(b []byte) *msgpReader { return &msgpReader{b: b} }

func (r *msgpReader) need(n int) error {
	if r.i+n > len(r.b) {
		return errors.New("msgpack short read")
	}

	return nil
}

// rest returns the bytes that haven't been consumed yet.
func (r *msgpReader) rest() []byte {
	return r.b[r.i:]
}

func (r *msgpReader) readByte() (byte, error) {
	err := r.need(1)
	if err != nil {
		return 0, err
	}

	c := r.b[r.i]
	r.i++
	return c, nil
}

func (r *msgpReader) readUint(n int) (uint32, error) {
	err := r.need(n)
	if err != nil {
		return 0, err
	}

	var v uint32
	switch n {
	case 1:
		v = uint32(r.b[r.i])
	case 2:
		v = uint32(binary.BigEndian.Uint16(r.b[r.i:]))
	case 4:
		v = binary.BigEndian.Uint32(r.b[r.i:])
	}

	r.i += n
	return v, nil
}

func (r *msgpReader) readUint32() (uint32, error) {
	c, err := r.readByte()
	if err != nil {
		return 0, err
	}

	if c != 0xce {
		return 0, fmt.Errorf("not a uint32 (0x%02x)", c)
	}

	return r.readUint(4)
}

func (r *msgpReader) readMapLen() (int, error) {
	c, err := r.readByte()
	if err != nil {
		return 0, err
	}

	switch {
	case c >= 0x80 && c <= 0x8f:
		return int(c & 0x0f), nil
	case c == 0xde:
		v, err := r.readUint(2)
		return int(v), err
	case c == 0xdf:
		v, err := r.readUint(4)
		return int(v), err
	}

	return 0, fmt.Errorf("not a map (0x%02x)", c)
}

func (r *msgpReader) readBin() ([]byte, error) {
	c, err := r.readByte()
	if err != nil {
		return nil, err
	}

	var n uint32

	switch c {
	case 0xc4:
		n, err = r.readUint(1)
	case 0xc5:
		n, err = r.readUint(2)
	case 0xc6:
		n, err = r.readUint(4)
	default:
		return nil, fmt.Errorf("not a bin (0x%02x)", c)
	}

	if err != nil {
		return nil, err
	}

	err = r.need(int(n))
	if err != nil {
		return nil, err
	}

	data := r.b[r.i : r.i+int(n)]
	r.i += int(n)
	return data, nil
}

func (r *msgpReader) readStr() (string, error) {
	c, err := r.readByte()
	if err != nil {
		return "", err
	}

	var n uint32

	switch {
	case c >= 0xa0 && c <= 0xbf:
		n = uint32(c & 0x1f)
	case c == 0xd9:
		n, err = r.readUint(1)
	case c == 0xda:
		n, err = r.readUint(2)
	case c == 0xdb:
		n, err = r.readUint(4)
	default:
		return "", fmt.Errorf("not a str (0x%02x)", c)
	}

	if err != nil {
		return "", err
	}

	err = r.need(int(n))
	if err != nil {
		return "", err
	}

	s := string(r.b[r.i : r.i+int(n)])
	r.i += int(n)
	return s, nil
}
