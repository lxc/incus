//go:build linux

package linux

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/xattr"
	"golang.org/x/sys/unix"
)

// Filesystem magic numbers.
const (
	FilesystemSuperMagicZfs = 0x2fc12fc1
)

// StatVFS retrieves Virtual File System (VFS) info about a path.
func StatVFS(path string) (*unix.Statfs_t, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
	if err != nil {
		return nil, err
	}

	return &st, nil
}

// DetectFilesystem returns the filesystem on which the passed-in path sits.
func DetectFilesystem(path string) (string, error) {
	fs, err := StatVFS(path)
	if err != nil {
		return "", err
	}

	return FSTypeToName(int32(fs.Type))
}

// FSTypeToName returns the name of the given fs type.
// The fsType is from the Type field of unix.Statfs_t. We use int32 so that this function behaves the same on both
// 32bit and 64bit platforms by requiring any 64bit FS types to be overflowed before being passed in. They will
// then be compared with equally overflowed FS type constant values.
func FSTypeToName(fsType int32) (string, error) {
	// This function is needed to allow FS type constants that overflow an int32 to be overflowed without a
	// compile error on 32bit platforms. This allows us to use any 64bit constants from the unix package on
	// both 64bit and 32bit platforms without having to define the constant in its rolled over form on 32bit.
	to32 := func(fsType int64) int32 {
		return int32(fsType)
	}

	switch fsType {
	case to32(unix.BTRFS_SUPER_MAGIC): // BTRFS' constant required overflowing to an int32.
		return "btrfs", nil
	case unix.TMPFS_MAGIC:
		return "tmpfs", nil
	case unix.EXT4_SUPER_MAGIC:
		return "ext4", nil
	case unix.XFS_SUPER_MAGIC:
		return "xfs", nil
	case unix.NFS_SUPER_MAGIC:
		return "nfs", nil
	case FilesystemSuperMagicZfs:
		return "zfs", nil
	}

	return fmt.Sprintf("0x%x", fsType), nil
}

func hasMountEntry(name string) int {
	// In case someone uses symlinks we need to look for the actual
	// mountpoint.
	actualPath, err := filepath.EvalSymlinks(name)
	if err != nil {
		return -1
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return -1
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			return -1
		}

		cleanPath := filepath.Clean(tokens[4])
		if cleanPath == actualPath {
			return 1
		}
	}

	return 0
}

// IsMountPoint returns true if path is a mount point.
func IsMountPoint(path string) bool {
	// If we find a mount entry, it is obviously a mount point.
	ret := hasMountEntry(path)
	if ret == 1 {
		return true
	}

	// Get the stat details.
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(path + "/..")
	if err != nil {
		return false
	}

	// If the directory has the same device as parent, then it's not a mountpoint.
	if stat.Sys().(*syscall.Stat_t).Dev == rootStat.Sys().(*syscall.Stat_t).Dev {
		return false
	}

	// Btrfs annoyingly uses a different Dev id for different subvolumes on the same mount.
	// So for btrfs, we require a matching mount entry in mountinfo.
	fs, _ := DetectFilesystem(path)
	if err == nil && fs == "btrfs" {
		return false
	}

	return true
}

// SyncFS will force a filesystem sync for the filesystem backing the provided path.
func SyncFS(path string) error {
	// Get us a file descriptor.
	fsFile, err := os.Open(path)
	if err != nil {
		return err
	}

	defer func() { _ = fsFile.Close() }()

	// Call SyncFS.
	return unix.Syncfs(int(fsFile.Fd()))
}

// PathNameEncode encodes a path string to be used as part of a file name.
// The encoding scheme replaces "-" with "--" and then "/" with "-".
func PathNameEncode(text string) string {
	return strings.Replace(strings.Replace(text, "-", "--", -1), "/", "-", -1)
}

// PathNameDecode decodes a string containing an encoded path back to its original form.
// The decoding scheme converts "-" back to "/" and "--" back to "-".
func PathNameDecode(text string) string {
	// This converts "--" to the null character "\0" first, to allow remaining "-" chars to be
	// converted back to "/" before making a final pass to convert "\0" back to original "-".
	return strings.Replace(strings.Replace(strings.Replace(text, "--", "\000", -1), "-", "/", -1), "\000", "-", -1)
}

// mountOption represents an individual mount option.
type mountOption struct {
	capture bool
	flag    uintptr
}

// mountFlagTypes represents a list of possible mount flags.
var mountFlagTypes = map[string]mountOption{
	"async":         {false, unix.MS_SYNCHRONOUS},
	"atime":         {false, unix.MS_NOATIME},
	"bind":          {true, unix.MS_BIND},
	"defaults":      {true, 0},
	"dev":           {false, unix.MS_NODEV},
	"diratime":      {false, unix.MS_NODIRATIME},
	"dirsync":       {true, unix.MS_DIRSYNC},
	"exec":          {false, unix.MS_NOEXEC},
	"lazytime":      {true, unix.MS_LAZYTIME},
	"mand":          {true, unix.MS_MANDLOCK},
	"noatime":       {true, unix.MS_NOATIME},
	"nodev":         {true, unix.MS_NODEV},
	"nodiratime":    {true, unix.MS_NODIRATIME},
	"noexec":        {true, unix.MS_NOEXEC},
	"nomand":        {false, unix.MS_MANDLOCK},
	"norelatime":    {false, unix.MS_RELATIME},
	"nostrictatime": {false, unix.MS_STRICTATIME},
	"nosuid":        {true, unix.MS_NOSUID},
	"rbind":         {true, unix.MS_BIND | unix.MS_REC},
	"relatime":      {true, unix.MS_RELATIME},
	"remount":       {true, unix.MS_REMOUNT},
	"ro":            {true, unix.MS_RDONLY},
	"rw":            {false, unix.MS_RDONLY},
	"strictatime":   {true, unix.MS_STRICTATIME},
	"suid":          {false, unix.MS_NOSUID},
	"sync":          {true, unix.MS_SYNCHRONOUS},
}

// ResolveMountOptions resolves the provided mount options.
func ResolveMountOptions(options []string) (uintptr, string) {
	mountFlags := uintptr(0)
	var mountOptions []string

	for i := range options {
		do, ok := mountFlagTypes[options[i]]
		if !ok {
			mountOptions = append(mountOptions, options[i])
			continue
		}

		if do.capture {
			mountFlags |= do.flag
		} else {
			mountFlags &= ^do.flag
		}
	}

	return mountFlags, strings.Join(mountOptions, ",")
}

// GetAllXattr retrieves all extended attributes associated with a file, directory or symbolic link.
func GetAllXattr(path string) (map[string]string, error) {
	xattrNames, err := xattr.LList(path)
	if err != nil {
		// Some filesystems don't support llistxattr() for various reasons.
		// Interpret this as a set of no xattrs, instead of an error.
		if errors.Is(err, unix.EOPNOTSUPP) {
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting extended attributes from %q: %w", path, err)
	}

	var xattrs = make(map[string]string, len(xattrNames))
	for _, xattrName := range xattrNames {
		value, err := xattr.LGet(path, xattrName)
		if err != nil {
			return nil, fmt.Errorf("Failed getting %q extended attribute from %q: %w", xattrName, path, err)
		}

		xattrs[xattrName] = string(value)
	}

	return xattrs, nil
}

// IsBlockdev checks if the provided file is a block device.
func IsBlockdev(fm os.FileMode) bool {
	return ((fm&os.ModeDevice != 0) && (fm&os.ModeCharDevice == 0))
}

// IsBlockdevPath checks if the provided path is a block device.
func IsBlockdevPath(pathName string) bool {
	sb, err := os.Stat(pathName)
	if err != nil {
		return false
	}

	fm := sb.Mode()
	return ((fm&os.ModeDevice != 0) && (fm&os.ModeCharDevice == 0))
}

// GetMountinfo tracks down the mount entry for the path and returns all MountInfo fields.
func GetMountinfo(path string) ([]string, error) {
	stat := &unix.Statx_t{}
	err := unix.Statx(0, path, 0, 0, stat)
	if err != nil {
		return nil, err
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			continue
		}

		if tokens[0] == fmt.Sprintf("%d", stat.Mnt_id) {
			return tokens, nil
		}
	}

	return nil, fmt.Errorf("No mountinfo entry found")
}
