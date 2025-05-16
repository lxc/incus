package cgroup

import (
	"errors"
)

// New setups a new CGroup abstraction using the provided read/writer.
func New(rw ReadWriter) (*CGroup, error) {
	if rw == nil {
		return nil, errors.New("A CGroup read/writer is required")
	}

	cg := CGroup{}
	cg.rw = rw

	return &cg, nil
}
