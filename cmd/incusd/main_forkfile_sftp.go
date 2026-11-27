package main

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/pkg/sftp"
	"golang.org/x/sys/unix"
)

// forkfileHandlers serves the local file system over SFTP, only ever opening regular files and directories.
type forkfileHandlers struct{}

// forkfileSFTPHandlers returns the handlers for the SFTP request server.
func forkfileSFTPHandlers() sftp.Handlers {
	h := forkfileHandlers{}

	return sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}
}

// forkfileOpen opens a regular file or a directory (older clients open those), refusing anything else.
func forkfileOpen(path string, flag int, mode os.FileMode) (*os.File, error) {
	// Check ahead of the open so that device nodes don't get opened at all.
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() && !info.IsDir() {
		return nil, &os.PathError{Op: "open", Path: path, Err: syscall.EPERM}
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	f, err := os.OpenFile(path, flag|unix.O_NONBLOCK|unix.O_NOCTTY, mode)
	if err != nil {
		return nil, err
	}

	// The path may have been replaced since the check.
	info, err = f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	if !info.Mode().IsRegular() && !info.IsDir() {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: syscall.EPERM}
	}

	return f, nil
}

// forkfileOpenFlags converts the SFTP open flags.
func forkfileOpenFlags(r *sftp.Request, flag int) int {
	pflags := r.Pflags()

	// The client sends the offsets, O_APPEND conflicts with WriteAt.
	if pflags.Creat {
		flag |= os.O_CREATE
	}

	if pflags.Trunc {
		flag |= os.O_TRUNC
	}

	if pflags.Excl {
		flag |= os.O_EXCL
	}

	return flag
}

// forkfileOpenMode returns the permissions for a file being created.
func forkfileOpenMode(r *sftp.Request) os.FileMode {
	// The request doesn't carry the attribute flags, only handle the usual case of permissions alone.
	if len(r.Attrs) == 4 {
		return os.FileMode(binary.BigEndian.Uint32(r.Attrs)) & os.ModePerm
	}

	return 0o644
}

// Fileread opens a file for reading.
func (forkfileHandlers) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	return forkfileOpen(r.Filepath, os.O_RDONLY, 0)
}

// Filewrite opens a file for writing.
func (forkfileHandlers) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	return forkfileOpen(r.Filepath, forkfileOpenFlags(r, os.O_WRONLY), forkfileOpenMode(r))
}

// OpenFile opens a file for reading and writing.
func (forkfileHandlers) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	return forkfileOpen(r.Filepath, forkfileOpenFlags(r, os.O_RDWR), forkfileOpenMode(r))
}

// Filecmd handles the requests changing the file system.
func (forkfileHandlers) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		flags := r.AttrFlags()
		attrs := r.Attributes()

		if flags.Size {
			err := os.Truncate(r.Filepath, int64(attrs.Size))
			if err != nil {
				return err
			}
		}

		if flags.Permissions {
			err := os.Chmod(r.Filepath, attrs.FileMode())
			if err != nil {
				return err
			}
		}

		if flags.UidGid {
			err := os.Chown(r.Filepath, int(attrs.UID), int(attrs.GID))
			if err != nil {
				return err
			}
		}

		if flags.Acmodtime {
			err := os.Chtimes(r.Filepath, attrs.AccessTime(), attrs.ModTime())
			if err != nil {
				return err
			}
		}

		return nil
	case "Rename":
		return os.Rename(r.Filepath, r.Target)
	case "Rmdir", "Remove":
		return os.Remove(r.Filepath)
	case "Mkdir":
		return os.Mkdir(r.Filepath, 0o755)
	case "Link":
		return os.Link(r.Filepath, r.Target)
	case "Symlink":
		return os.Symlink(r.Filepath, r.Target)
	}

	return sftp.ErrSSHFxOpUnsupported
}

// PosixRename renames a file, replacing the target.
func (forkfileHandlers) PosixRename(r *sftp.Request) error {
	return os.Rename(r.Filepath, r.Target)
}

// StatVFS returns the file system statistics.
func (forkfileHandlers) StatVFS(r *sftp.Request) (*sftp.StatVFS, error) {
	stat := unix.Statfs_t{}

	err := unix.Statfs(r.Filepath, &stat)
	if err != nil {
		return nil, err
	}

	return &sftp.StatVFS{
		Bsize:   uint64(stat.Bsize),
		Frsize:  uint64(stat.Frsize),
		Blocks:  stat.Blocks,
		Bfree:   stat.Bfree,
		Bavail:  stat.Bavail,
		Files:   stat.Files,
		Ffree:   stat.Ffree,
		Favail:  stat.Ffree,
		Flag:    uint64(stat.Flags),
		Namemax: uint64(stat.Namelen),
	}, nil
}

// Filelist handles directory listings and stat requests.
func (forkfileHandlers) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		// O_DIRECTORY fails on anything else without blocking.
		f, err := os.OpenFile(r.Filepath, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}

		return &forkfileDir{f: f}, nil
	case "Stat":
		info, err := os.Stat(r.Filepath)
		if err != nil {
			return nil, err
		}

		return forkfileInfo{info}, nil
	}

	return nil, sftp.ErrSSHFxOpUnsupported
}

// Lstat returns the details of a file without following symlinks.
func (forkfileHandlers) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	info, err := os.Lstat(r.Filepath)
	if err != nil {
		return nil, err
	}

	return forkfileInfo{info}, nil
}

// Readlink returns the target of a symlink.
func (forkfileHandlers) Readlink(path string) (string, error) {
	return os.Readlink(path)
}

// forkfileInfo is the listing of a single file.
type forkfileInfo []os.FileInfo

// ListAt copies the file details.
func (i forkfileInfo) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(i)) {
		return 0, io.EOF
	}

	n := copy(ls, i[offset:])
	if n < len(ls) {
		return n, io.EOF
	}

	return n, nil
}

// forkfileDir lists an open directory in order.
type forkfileDir struct {
	f *os.File
}

// ListAt returns the next entries, the server only ever reads forward.
func (d *forkfileDir) ListAt(ls []os.FileInfo, _ int64) (int, error) {
	entries, err := d.f.Readdir(len(ls))

	return copy(ls, entries), err
}

// Close closes the directory.
func (d *forkfileDir) Close() error {
	return d.f.Close()
}
