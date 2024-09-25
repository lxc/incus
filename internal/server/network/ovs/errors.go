package ovs

import (
	"fmt"

	ovsdbClient "github.com/ovn-org/libovsdb/client"
)

// ErrExists indicates that a DB record already exists.
var ErrExists = fmt.Errorf("object already exists")

// ErrNotFound indicates that a DB record doesn't exist.
var ErrNotFound = ovsdbClient.ErrNotFound

// ErrNotManaged indicates that a DB record wasn't created by Incus.
var ErrNotManaged = fmt.Errorf("object not incus-managed")
