package db

import (
	"errors"
)

var (
	// ErrAlreadyDefined happens when the given entry already exists,
	// for example a container.
	ErrAlreadyDefined = errors.New("The record already exists")

	// ErrNoClusterMember is used to indicate no cluster member has been found for a resource.
	ErrNoClusterMember = errors.New("No cluster member found")
)
