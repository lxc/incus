package linux

import (
	"cmp"
	"encoding/binary"
	"errors"
	"slices"

	"golang.org/x/sys/unix"
)

const posixACLXattr = "system.posix_acl_access"

// POSIX ACL xattr encoding (include/uapi/linux/posix_acl_xattr.h).
const (
	posixACLVersion = 2

	aclUserObj  = 0x01
	aclUser     = 0x02
	aclGroupObj = 0x04
	aclGroup    = 0x08
	aclMask     = 0x10
	aclOther    = 0x20

	aclUndefinedID = 0xFFFFFFFF
)

type aclEntry struct {
	tag  uint16
	perm uint16
	id   uint32
}

// GrantPosixACLUser grants the provided permissions ("rwx" bits) to a uid on path,
// creating or extending the POSIX access ACL as needed.
func GrantPosixACLUser(path string, uid uint32, perm uint16) error {
	var entries []aclEntry

	// Retrieve the current ACL if any.
	sz, err := unix.Getxattr(path, posixACLXattr, nil)
	if err != nil && !errors.Is(err, unix.ENODATA) {
		return err
	}

	if err == nil {
		buf := make([]byte, sz)

		sz, err = unix.Getxattr(path, posixACLXattr, buf)
		if err != nil {
			return err
		}

		if sz < 4 || (sz-4)%8 != 0 || binary.LittleEndian.Uint32(buf) != posixACLVersion {
			return errors.New("Invalid POSIX ACL xattr")
		}

		for off := 4; off < sz; off += 8 {
			entries = append(entries, aclEntry{
				tag:  binary.LittleEndian.Uint16(buf[off:]),
				perm: binary.LittleEndian.Uint16(buf[off+2:]),
				id:   binary.LittleEndian.Uint32(buf[off+4:]),
			})
		}
	} else {
		// No ACL set, derive the base entries from the file mode.
		var st unix.Stat_t

		err = unix.Stat(path, &st)
		if err != nil {
			return err
		}

		entries = []aclEntry{
			{tag: aclUserObj, perm: uint16(st.Mode>>6) & 0o7, id: aclUndefinedID},
			{tag: aclGroupObj, perm: uint16(st.Mode>>3) & 0o7, id: aclUndefinedID},
			{tag: aclOther, perm: uint16(st.Mode) & 0o7, id: aclUndefinedID},
		}
	}

	// Add or extend the user entry.
	idx := slices.IndexFunc(entries, func(e aclEntry) bool { return e.tag == aclUser && e.id == uid })
	if idx >= 0 {
		entries[idx].perm |= perm
	} else {
		entries = append(entries, aclEntry{tag: aclUser, perm: perm, id: uid})
	}

	// Recompute the mask as the union of all group-class entries.
	mask := uint16(0)
	maskIdx := -1
	for i, e := range entries {
		switch e.tag {
		case aclUser, aclGroupObj, aclGroup:
			mask |= e.perm
		case aclMask:
			maskIdx = i
		}
	}

	if maskIdx >= 0 {
		entries[maskIdx].perm = mask
	} else {
		entries = append(entries, aclEntry{tag: aclMask, perm: mask, id: aclUndefinedID})
	}

	// The kernel expects entries ordered by tag, then qualifier.
	slices.SortStableFunc(entries, func(a, b aclEntry) int {
		if a.tag != b.tag {
			return cmp.Compare(a.tag, b.tag)
		}

		return cmp.Compare(a.id, b.id)
	})

	buf := make([]byte, 4+8*len(entries))
	binary.LittleEndian.PutUint32(buf, posixACLVersion)
	for i, e := range entries {
		off := 4 + 8*i
		binary.LittleEndian.PutUint16(buf[off:], e.tag)
		binary.LittleEndian.PutUint16(buf[off+2:], e.perm)
		binary.LittleEndian.PutUint32(buf[off+4:], e.id)
	}

	return unix.Setxattr(path, posixACLXattr, buf, 0)
}
