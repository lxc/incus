package db

import (
	"errors"
	"time"
)

// CheckTime returns an error when a time can't be stored in the database and read back.
func CheckTime(t time.Time) error {
	if t.IsZero() {
		return nil
	}

	year := t.UTC().Year()
	if year < 1 || year > 9999 {
		return errors.New("Dates must fall between years 1 and 9999")
	}

	return nil
}
