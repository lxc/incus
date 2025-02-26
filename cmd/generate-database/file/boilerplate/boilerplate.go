package boilerplate

import (
	"errors"
)

var (
	// ErrNotFound is the error returned, if the entity is not found in the DB.
	ErrNotFound = errors.New("Not found")

	// ErrConflict is the error returned, if the adding or updating an entity
	// causes a conflict with an existing entity.
	ErrConflict = errors.New("Conflict")
)

var mapErr = defaultMapErr

func defaultMapErr(err error, entity string) error {
	return err
}
