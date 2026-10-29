// Package nbd attaches NBD exports to kernel block devices.
package nbd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// Fixed newstyle handshake constants.
const (
	magic      = 0x4e42444d41474943
	optMagic   = 0x49484156454f5054
	replyMagic = 0x3e889045565a9

	flagFixedNewstyle = 1 << 0
	flagNoZeroes      = 1 << 1

	optGo = 7

	repAck    = 1
	repInfo   = 3
	repErrBit = 1 << 31

	infoExport    = 0
	infoBlockSize = 3
)

// Export describes a negotiated NBD export.
type Export struct {
	Size      uint64
	Flags     uint16
	BlockSize uint64
}

// Negotiate performs the fixed newstyle handshake for the default export of the server behind conn.
// On success the connection is in the transmission phase and can be handed to the kernel.
func Negotiate(conn net.Conn) (*Export, error) {
	var hdr struct {
		Magic    uint64
		OptMagic uint64
		Flags    uint16
	}

	err := binary.Read(conn, binary.BigEndian, &hdr)
	if err != nil {
		return nil, fmt.Errorf("Failed reading NBD handshake: %w", err)
	}

	if hdr.Magic != magic || hdr.OptMagic != optMagic {
		return nil, errors.New("Unexpected NBD handshake")
	}

	if hdr.Flags&flagFixedNewstyle == 0 {
		return nil, errors.New("NBD server doesn't support fixed newstyle negotiation")
	}

	clientFlags := uint32(flagFixedNewstyle)
	if hdr.Flags&flagNoZeroes != 0 {
		clientFlags |= flagNoZeroes
	}

	err = binary.Write(conn, binary.BigEndian, clientFlags)
	if err != nil {
		return nil, fmt.Errorf("Failed sending NBD client flags: %w", err)
	}

	// Request the default export along with its block size constraints.
	data := binary.BigEndian.AppendUint32(nil, 0)
	data = binary.BigEndian.AppendUint16(data, 1)
	data = binary.BigEndian.AppendUint16(data, infoBlockSize)

	opt := binary.BigEndian.AppendUint64(nil, optMagic)
	opt = binary.BigEndian.AppendUint32(opt, optGo)
	opt = binary.BigEndian.AppendUint32(opt, uint32(len(data)))
	opt = append(opt, data...)

	_, err = conn.Write(opt)
	if err != nil {
		return nil, fmt.Errorf("Failed sending NBD export request: %w", err)
	}

	export := &Export{BlockSize: 512}
	haveExport := false

	for {
		var rep struct {
			Magic  uint64
			Option uint32
			Type   uint32
			Length uint32
		}

		err = binary.Read(conn, binary.BigEndian, &rep)
		if err != nil {
			return nil, fmt.Errorf("Failed reading NBD option reply: %w", err)
		}

		if rep.Magic != replyMagic || rep.Option != optGo {
			return nil, errors.New("Unexpected NBD option reply")
		}

		payload := make([]byte, rep.Length)
		_, err = io.ReadFull(conn, payload)
		if err != nil {
			return nil, fmt.Errorf("Failed reading NBD option reply: %w", err)
		}

		if rep.Type&repErrBit != 0 {
			return nil, fmt.Errorf("NBD server rejected the export (error %#x): %s", rep.Type&^repErrBit, string(payload))
		}

		switch rep.Type {
		case repAck:
			if !haveExport {
				return nil, errors.New("NBD server didn't describe the export")
			}

			return export, nil
		case repInfo:
			if len(payload) < 2 {
				return nil, errors.New("Truncated NBD export information")
			}

			switch binary.BigEndian.Uint16(payload) {
			case infoExport:
				if len(payload) < 12 {
					return nil, errors.New("Truncated NBD export information")
				}

				export.Size = binary.BigEndian.Uint64(payload[2:])
				export.Flags = binary.BigEndian.Uint16(payload[10:])
				haveExport = true
			case infoBlockSize:
				if len(payload) < 14 {
					return nil, errors.New("Truncated NBD block size information")
				}

				// The kernel needs at least 512 byte blocks, larger minimums must be honored.
				minBlock := uint64(binary.BigEndian.Uint32(payload[2:]))
				if minBlock > export.BlockSize {
					export.BlockSize = minBlock
				}
			}
		default:
			return nil, fmt.Errorf("Unexpected NBD option reply type %d", rep.Type)
		}
	}
}
