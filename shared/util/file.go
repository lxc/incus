package util

import (
	"io/fs"
	"strconv"
)

// ParseMode parses a Unix file mode string into an `fs.FileMode`.
func ParseMode(s string) (fs.FileMode, error) {
	m, err := strconv.ParseUint(s, 8, 12)
	if err != nil {
		return 0, err
	}

	mode := fs.FileMode(m & 0o777)
	if m&0o1000 != 0 {
		mode |= fs.ModeSticky
	}

	if m&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}

	if m&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}

	return mode, nil
}
