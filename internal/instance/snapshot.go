package instance

import (
	"strings"
)

// SnapshotDelimiter is used to separate instance name from snapshot name.
const SnapshotDelimiter = "/"

// IsSnapshot checks if provided name is a snapshot name.
func IsSnapshot(name string) bool {
	return strings.Contains(name, SnapshotDelimiter)
}
