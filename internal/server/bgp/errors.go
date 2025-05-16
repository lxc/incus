package bgp

import (
	"errors"
)

// ErrPrefixNotFound is returned when a user provided prefix couldn't be found.
var ErrPrefixNotFound = errors.New("Prefix not found")

// ErrPeerNotFound is returned when a user provided peer couldn't be found.
var ErrPeerNotFound = errors.New("Peer not found")

// ErrBadRouterID is returned when an invalid router-id is provided.
var ErrBadRouterID = errors.New("Invalid router-id (must be IPv4 address")
