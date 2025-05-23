package ovs

import (
	"errors"

	ovsdbClient "github.com/ovn-org/libovsdb/client"
)

// ErrExists indicates that a DB record already exists.
var ErrExists = errors.New("object already exists")

// ErrNotFound indicates that a DB record doesn't exist.
var ErrNotFound = ovsdbClient.ErrNotFound

// ErrNotManaged indicates that a DB record wasn't created by Incus.
var ErrNotManaged = errors.New("object not incus-managed")
