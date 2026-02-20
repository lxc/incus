package drivers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/logger"
)

var fanotifyLoaded bool

type fanotify struct {
	common

	fd int
}

type fanotifyEventInfoHeader struct {
	InfoType uint8
	Pad      uint8
	Len      uint16
}

type fanotifyEventInfoFid struct {
	fanotifyEventInfoHeader
	FSID uint64
}

func (d *fanotify) Name() string {
	return "fanotify"
}

func (d *fanotify) load(ctx context.Context) error {
	if fanotifyLoaded {
		return nil
	}

	var err error

	d.fd, err = unix.FanotifyInit(unix.FAN_CLOEXEC|unix.FAN_REPORT_DFID_NAME, unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("Failed to initialize fanotify: %w", err)
	}

	err = unix.FanotifyMark(d.fd, unix.FAN_MARK_ADD|unix.FAN_MARK_FILESYSTEM, unix.FAN_CREATE|unix.FAN_DELETE|unix.FAN_ONDIR, unix.AT_FDCWD, d.prefixPath)
	if err != nil {
		_ = unix.Close(d.fd)
		return fmt.Errorf("Failed to watch directory %q: %w", d.prefixPath, err)
	}

	fd, err := unix.Open(d.prefixPath, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(d.fd)
		return fmt.Errorf("Failed to open directory %q: %w", d.prefixPath, err)
	}

	go func() {
		<-ctx.Done()
		_ = unix.Close(d.fd)
		fanotifyLoaded = false
	}()

	go d.getEvents(ctx, fd)

	fanotifyLoaded = true

	return nil
}

func (d *fanotify) getEvents(ctx context.Context, mountFd int) {
	for {
		buf := make([]byte, 4096)

		// Read enough bytes to handle multiple event records returned in one read call.
		n, err := unix.Read(d.fd, buf)
		if err != nil {
			// Stop listening for events as the fanotify fd has been closed due to cleanup.
			if ctx.Err() != nil || errors.Is(err, unix.EBADF) {
				_ = unix.Close(mountFd)
				return
			}

			d.logger.Error("Failed to read event", logger.Ctx{"err": err})
			continue
		}

		processed := 0
		for processed < n {
			rd := bytes.NewReader(buf[processed:])

			event := unix.FanotifyEventMetadata{}
			err = binary.Read(rd, binary.LittleEndian, &event)
			if err != nil {
				d.logger.Error("Failed to read event metadata", logger.Ctx{"err": err})
				continue
			}

			processed += int(event.Event_len)

			// Kernel queue overflow means events were dropped before userspace could read them.
			if event.Mask&unix.FAN_Q_OVERFLOW != 0 {
				d.logger.Warn("fanotify queue overflow detected, events may have been dropped")
			}

			// Read event info fid
			fid := fanotifyEventInfoFid{}

			err = binary.Read(rd, binary.LittleEndian, &fid)
			if err != nil {
				d.logger.Error("Failed to read event fid", logger.Ctx{"err": err})
				continue
			}

			// Although unix.FileHandle exists, it cannot be used with binary.Read() as the
			// variables inside are not exported.
			type fileHandleInfo struct {
				Bytes uint32
				Type  int32
			}

			// Read file handle information
			fhInfo := fileHandleInfo{}

			err = binary.Read(rd, binary.LittleEndian, &fhInfo)
			if err != nil {
				d.logger.Error("Failed to read file handle info", logger.Ctx{"err": err})
				continue
			}

			// Read file handle
			fileHandle := make([]byte, fhInfo.Bytes)

			err = binary.Read(rd, binary.LittleEndian, fileHandle)
			if err != nil {
				d.logger.Error("Failed to read file handle", logger.Ctx{"err": err})
				continue
			}

			fh := unix.NewFileHandle(fhInfo.Type, fileHandle)

			fd, err := unix.OpenByHandleAt(mountFd, fh, 0)
			if err != nil {
				if !errors.Is(err, unix.ESTALE) {
					d.logger.Error("Failed to open file", logger.Ctx{"err": err})
				}

				continue
			}

			// Determine the directory of the created or deleted file.
			target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
			_ = unix.Close(fd)
			if err != nil {
				d.logger.Error("Failed to read symlink", logger.Ctx{"err": err})
				continue
			}

			// If the target file has been deleted, the returned value might contain a " (deleted)" suffix.
			// This needs to be removed.
			target = strings.TrimSuffix(target, " (deleted)")

			// The file handle is followed by a null terminated string that identifies the
			// created/deleted directory entry name.
			sb := strings.Builder{}
			sb.WriteString(target + "/")

			for {
				b, err := rd.ReadByte()
				if err != nil || b == 0 {
					break
				}

				err = sb.WriteByte(b)
				if err != nil {
					break
				}
			}

			eventPath := filepath.Clean(sb.String())

			var action Event
			if event.Mask&unix.FAN_CREATE != 0 {
				action = Add
			} else if event.Mask&unix.FAN_DELETE != 0 || event.Mask&unix.FAN_DELETE_SELF != 0 {
				action = Remove
			} else {
				continue
			}

			d.mu.Lock()
			for identifier, f := range d.watches[eventPath] {
				ret := f(eventPath, action.String())
				if !ret {
					delete(d.watches[eventPath], identifier)
					if len(d.watches[eventPath]) == 0 {
						delete(d.watches, eventPath)
					}
				}
			}

			d.mu.Unlock()
		}
	}
}
