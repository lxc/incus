package bgp

import (
	"fmt"
)

// ErrPrefixNotFound is returned when a user provided prefix couldn't be found.
var ErrPrefixNotFound = fmt.Errorf("Prefix not found")

// ErrPeerNotFound is returned when a user provided peer couldn't be found.
var ErrPeerNotFound = fmt.Errorf("Peer not found")

// ErrBadRouterID is returned when an invalid router-id is provided.
var ErrBadRouterID = fmt.Errorf("Invalid router-id (must be IPv4 address")
