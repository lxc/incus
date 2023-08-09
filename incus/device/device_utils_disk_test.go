package device

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/shared/idmap"
)

func TestDiskAddRootUserNSEntry(t *testing.T) {
	// Check adds a combined uid/gid root entry to an empty list.
	var idmaps []idmap.IdmapEntry
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected := []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, idmaps, expected)

	// Check doesn't add another one if an existing combined entry exists.
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	assert.Equal(t, idmaps, expected)

	// Check adds a root gid entry if root uid entry already exists.
	idmaps = []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, idmaps, expected)

	// Check adds a root uid entry if root gid entry already exists.
	idmaps = []idmap.IdmapEntry{
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.IdmapEntry{
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, idmaps, expected)
}
