package uefi

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

// reader is a wrapper to help reading OVMF files.
type reader struct {
	data []byte
	off  int
}

// newReader returns a reader over the given data.
func newReader(data []byte) *reader {
	return &reader{data: data}
}

// pos returns the reader’s position.
func (r *reader) pos() int {
	return r.off
}

// rem returns the reader’s remaining bytes.
func (r *reader) rem() int {
	return len(r.data) - r.off
}

// eof returns whether the reader has reached the end.
func (r *reader) eof() bool {
	return r.rem() <= 0
}

// seek moves the reader’s cursor.
func (r *reader) seek(off int) error {
	if off < 0 || off > len(r.data) {
		return fmt.Errorf("seek outside input: 0x%x", off)
	}

	r.off = off
	return nil
}

// skip skips bytes.
func (r *reader) skip(n int) error {
	if n < 0 || r.off+n > len(r.data) {
		return errUnexpectedData
	}

	r.off += n
	return nil
}

// read reads raw bytes.
func (r *reader) read(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.data) {
		return nil, fmt.Errorf("unexpected EOF at 0x%x reading %d bytes", r.off, n)
	}

	out := r.data[r.off : r.off+n]
	r.off += n
	return out, nil
}

// readB8 reads an 8-bit boolean.
func (r *reader) readB8() (bool, error) {
	b, err := r.read(1)
	if err != nil {
		return false, err
	}

	v := b[0]
	if v != 0 && v != 1 {
		return false, errUnexpectedData
	}

	return v != 0, nil
}

// readU8 reads an 8-bit unsigned integer.
func (r *reader) readU8() (uint8, error) {
	b, err := r.read(1)
	if err != nil {
		return 0, err
	}

	return b[0], nil
}

// readE8 reads an 8-bit unsigned integer and looks for it in the given map. `fail` controls
// whether the absence of the key in the map is an error or not.
func (r *reader) readE8(enum map[uint8]string, fail bool) (string, error) {
	k, err := r.readU8()
	if err != nil {
		return "", err
	}

	v, ok := enum[k]
	if !ok {
		if fail {
			return "", errUnexpectedData
		}

		return fmt.Sprintf("0x%x", k), nil
	}

	return v, nil
}

// readU16 reads a 16-bit unsigned integer.
func (r *reader) readU16() (uint16, error) {
	b, err := r.read(2)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint16(b), nil
}

// readE16 reads a 16-bit unsigned integer and looks for it in the given map. `fail` controls
// whether the absence of the key in the map is an error or not.
func (r *reader) readE16(enum map[uint16]string, fail bool) (string, error) {
	k, err := r.readU16()
	if err != nil {
		return "", err
	}

	v, ok := enum[k]
	if !ok {
		if fail {
			return "", errUnexpectedData
		}

		return fmt.Sprintf("0x%x", k), nil
	}

	return v, nil
}

// readU32 reads a 32-bit unsigned integer.
func (r *reader) readU32() (uint32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint32(b), nil
}

// readE32 reads a 32-bit unsigned integer and looks for it in the given map. `fail` controls
// whether the absence of the key in the map is an error or not.
func (r *reader) readE32(enum map[uint32]string, fail bool) (string, error) {
	k, err := r.readU32()
	if err != nil {
		return "", err
	}

	v, ok := enum[k]
	if !ok {
		if fail {
			return "", errUnexpectedData
		}

		return fmt.Sprintf("0x%x", k), nil
	}

	return v, nil
}

// readU64 reads a 64-bit unsigned integer.
func (r *reader) readU64() (uint64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint64(b), nil
}

// readU64BE reads a big-endian 64-bit unsigned integer.
func (r *reader) readU64BE() (uint64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint64(b), nil
}

// readGUID reads a GUID.
func (r *reader) readGUID() (string, error) {
	b, err := r.read(16)
	if err != nil {
		return "", err
	}

	return formatGUID(b), nil
}

// readGUIDBE reads a big-endian GUID.
func (r *reader) readGUIDBE() (string, error) {
	b, err := r.read(16)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// readEISA reads an EISA type ID.
func (r *reader) readEISA() (string, error) {
	b, err := r.readU32()
	if err != nil {
		return "", err
	}

	c1 := byte(b>>10&0x1f) + 'A' - 1
	c2 := byte(b>>5&0x1f) + 'A' - 1
	c3 := byte(b&0x1f) + 'A' - 1
	if c1 < 'A' || c1 > 'Z' || c2 < 'A' || c2 > 'Z' || c3 < 'A' || c3 > 'Z' {
		return fmt.Sprintf("0x%08x", b), nil
	}

	return fmt.Sprintf("%c%c%c%04X", c1, c2, c3, uint16(b>>16)), nil
}

// readEUI64 reads an EUI64 address.
func (r *reader) readEUI64() (string, error) {
	b, err := r.read(8)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%02x-%02x-%02x-%02x-%02x-%02x-%02x-%02x", b[7], b[6], b[5], b[4], b[3], b[2], b[1], b[0]), nil
}

// readEUI64BE reads a big-endian EUI64 address.
func (r *reader) readEUI64BE() (string, error) {
	b, err := r.read(8)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%02x-%02x-%02x-%02x-%02x-%02x-%02x-%02x", b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7]), nil
}

func (r *reader) _readZ8(n ...int) (string, bool, error) {
	var b []byte
	maxSize := -1
	nul := true
	if len(n) > 0 {
		maxSize = n[0]
	}

	if maxSize == 0 {
		return "", false, nil
	}

	v, err := r.readU8()
	if err != nil {
		return "", false, err
	}

	for v != 0 {
		b = append(b, v)
		if len(b) == maxSize {
			nul = false
			break
		}

		v, err = r.readU8()
		if err != nil {
			return "", false, err
		}
	}

	// If we were asked to read more, simply skip the remaining bytes.
	rem := maxSize - len(b) - 1
	if rem > 0 {
		err = r.skip(rem)
		if err != nil {
			return "", false, err
		}
	}

	return string(b), nul, nil
}

// readZ8 reads an optionally NUL-terminated ASCII/UTF8 string up to the given size, if specified.
func (r *reader) readZ8(n ...int) (string, error) {
	s, _, err := r._readZ8(n...)
	return s, err
}

// readZn8 reads a NUL-terminated ASCII/UTF8 string up to the given size, if specified.
func (r *reader) readZn8(n ...int) (string, error) {
	s, nul, err := r._readZ8(n...)
	if !nul {
		return "", errUnexpectedData
	}

	return s, err
}

func (r *reader) _readZ16(n ...int) (string, bool, error) {
	var b []uint16
	maxSize := -1
	nul := true
	if len(n) > 0 {
		maxSize = n[0]
	}

	if maxSize == 0 {
		return "", false, nil
	}

	v, err := r.readU16()
	if err != nil {
		return "", false, err
	}

	for v != 0 {
		b = append(b, v)
		if len(b) == maxSize {
			nul = false
			break
		}

		v, err = r.readU16()
		if err != nil {
			return "", false, err
		}
	}

	// If we were asked to read more, simply skip the remaining bytes.
	rem := maxSize - len(b) - 1
	if rem > 0 {
		err = r.skip(2 * rem)
		if err != nil {
			return "", false, err
		}
	}

	return string(utf16.Decode(b)), nul, nil
}

// readZ16 reads an optionally NUL-terminated UTF16 string up to the given size, if specified.
func (r *reader) readZ16(n ...int) (string, error) {
	s, _, err := r._readZ16(n...)
	return s, err
}

// readZn16 reads a NUL-terminated UTF16 string up to the given size, if specified.
func (r *reader) readZn16(n ...int) (string, error) {
	s, nul, err := r._readZ16(n...)
	if !nul {
		return "", errUnexpectedData
	}

	return s, err
}

// readTimestamp reads an EFI time. We consider times to be expressed in UTC and ignore nanoseconds,
// because all our call sites do so. This function returns a nil time if all the read bytes are 0.
func (r *reader) readTimestamp() (*time.Time, error) {
	year, err := r.readU16()
	if err != nil {
		return nil, err
	}

	month, err := r.readU8()
	if err != nil {
		return nil, err
	}

	day, err := r.readU8()
	if err != nil {
		return nil, err
	}

	hour, err := r.readU8()
	if err != nil {
		return nil, err
	}

	minute, err := r.readU8()
	if err != nil {
		return nil, err
	}

	second, err := r.readU8()
	if err != nil {
		return nil, err
	}

	// Pad1+Nanosecond+TimeZone+Daylight+Pad2.
	err = r.skip(9)
	if err != nil {
		return nil, err
	}

	if year == 0 && month == 0 && day == 0 && hour == 0 && minute == 0 && second == 0 {
		return nil, nil
	}

	t := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.UTC)
	return &t, nil
}

// writer is a wrapper to help writing OVMF files.
type writer struct {
	data []byte
}

// newWriter returns a writer.
func newWriter() *writer {
	return &writer{}
}

// size returns the writer’s size.
func (w *writer) size() int {
	return len(w.data)
}

// write writes raw bytes.
func (w *writer) write(b []byte) error {
	w.data = append(w.data, b...)
	return nil
}

// writeU8 writes an 8-bit unsigned integer.
func (w *writer) writeU8(v uint8) error {
	w.data = append(w.data, v)
	return nil
}

// writeU16 writes a 16-bit unsigned integer.
func (w *writer) writeU16(v uint16) error {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	w.data = append(w.data, b...)
	return nil
}

// writeU16At writes a 16-bit unsigned integer at the given position.
func (w *writer) writeU16At(v uint16, pos int) error {
	binary.LittleEndian.PutUint16(w.data[pos:], v)
	return nil
}

// writeU32 writes a 32-bit unsigned integer.
func (w *writer) writeU32(v uint32) error {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	w.data = append(w.data, b...)
	return nil
}

// writeU64 writes a 64-bit unsigned integer.
func (w *writer) writeU64(v uint64) error {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	w.data = append(w.data, b...)
	return nil
}

// writeGUID reads a GUID.
func (w *writer) writeGUID(guid string) error {
	b, err := hex.DecodeString(strings.ReplaceAll(guid, "-", ""))
	if err != nil {
		return err
	}

	w.data = append(append(w.data, b[3], b[2], b[1], b[0], b[5], b[4], b[7], b[6]), b[8:]...)
	return nil
}

// writeZ8 writes an ASCII/UTF8 string.
func (w *writer) writeZ8(s string, n ...int) error {
	size := len(s)
	if len(n) > 0 {
		size = n[0]
	}

	b := make([]byte, size)
	copy(b, s)
	w.data = append(w.data, b...)
	return nil
}

// writeZ16 writes an UTF16 string.
func (w *writer) writeZ16(s string, n ...int) error {
	size := len(s) * 2
	if len(n) > 0 {
		size = n[0] * 2
	}

	b := make([]byte, size)
	for i, c := range utf16.Encode([]rune(s)) {
		b[i*2] = byte(c)
		b[i*2+1] = byte(c >> 8)
	}

	w.data = append(w.data, b...)
	return nil
}

// writeZ16 writes a NUL-terminated UTF16 string.
func (w *writer) writeZn16(s string, n ...int) error {
	return w.writeZ16(s + "\x00")
}

// writeTimestamp writes an EFI time. We consider times to be expressed in UTC and ignore
// nanoseconds, because all our call sites do so. This function returns a nil time if all the read
// bytes are 0.
func (w *writer) writeTimestamp(t *time.Time) error {
	if t == nil {
		return w.write([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	}

	err := w.writeU16(uint16(t.Year()))
	if err != nil {
		return err
	}

	err = w.writeU8(uint8(t.Month()))
	if err != nil {
		return err
	}

	err = w.writeU8(uint8(t.Day()))
	if err != nil {
		return err
	}

	err = w.writeU8(uint8(t.Hour()))
	if err != nil {
		return err
	}

	err = w.writeU8(uint8(t.Minute()))
	if err != nil {
		return err
	}

	err = w.writeU8(uint8(t.Second()))
	if err != nil {
		return err
	}

	// Pad1+Nanosecond+TimeZone+Daylight+Pad2.
	return w.write([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0})
}
