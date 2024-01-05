package device

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/shared/idmap"
)

func TestDiskAddRootUserNSEntry(t *testing.T) {
	// Check adds a combined uid/gid root entry to an empty list.
	var idmaps []idmap.Entry
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected := []idmap.Entry{
		{
			IsUID:    true,
			IsGID:    true,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
	}

	assert.Equal(t, idmaps, expected)

	// Check doesn't add another one if an existing combined entry exists.
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	assert.Equal(t, idmaps, expected)

	// Check adds a root gid entry if root uid entry already exists.
	idmaps = []idmap.Entry{
		{
			IsUID:    true,
			IsGID:    false,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.Entry{
		{
			IsUID:    true,
			IsGID:    false,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
		{
			IsUID:    false,
			IsGID:    true,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
	}

	assert.Equal(t, idmaps, expected)

	// Check adds a root uid entry if root gid entry already exists.
	idmaps = []idmap.Entry{
		{
			IsUID:    false,
			IsGID:    true,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.Entry{
		{
			IsUID:    false,
			IsGID:    true,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
		{
			IsUID:    true,
			IsGID:    false,
			HostID:   65534,
			MapRange: 1,
			NSID:     0,
		},
	}

	assert.Equal(t, idmaps, expected)
}
