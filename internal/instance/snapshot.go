package instance

import (
	"strings"
)

const SnapshotDelimiter = "/"

func IsSnapshot(name string) bool {
	return strings.Contains(name, SnapshotDelimiter)
}
