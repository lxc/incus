package project

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/shared/idmap"
)

func TestParseHostIDMapRange(t *testing.T) {
	for _, mode := range []string{"uid", "gid", "both"} {
		var isUID, isGID bool
		switch mode {
		case "uid":
			isUID = true
		case "gid":
			isGID = true
		case "both":
			isUID = true
			isGID = true
		}

		idmaps, err := parseHostIDMapRange(isUID, isGID, "foo")
		assert.NotErrorIs(t, err, nil)
		assert.Nil(t, idmaps)

		idmaps, err = parseHostIDMapRange(isUID, isGID, "1000")
		expected := []idmap.Entry{
			{
				IsUID:    isUID,
				IsGID:    isGID,
				HostID:   1000,
				MapRange: 1,
				NSID:     -1,
			},
		}

		assert.ErrorIs(t, err, nil)
		assert.Equal(t, idmaps, expected)

		idmaps, err = parseHostIDMapRange(isUID, isGID, "1000-1001")
		expected = []idmap.Entry{
			{
				IsUID:    isUID,
				IsGID:    isGID,
				HostID:   1000,
				MapRange: 2,
				NSID:     -1,
			},
		}

		assert.ErrorIs(t, err, nil)
		assert.Equal(t, idmaps, expected)

		idmaps, err = parseHostIDMapRange(isUID, isGID, "1000-1001,1002")
		expected = []idmap.Entry{
			{
				IsUID:    isUID,
				IsGID:    isGID,
				HostID:   1000,
				MapRange: 2,
				NSID:     -1,
			},
			{
				IsUID:    isUID,
				IsGID:    isGID,
				HostID:   1002,
				MapRange: 1,
				NSID:     -1,
			},
		}

		assert.ErrorIs(t, err, nil)
		assert.Equal(t, idmaps, expected)
	}
}
