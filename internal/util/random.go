package util

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// RandomHexString returns a random string of hexadecimal characters.
func RandomHexString(length int) (string, error) {
	buf := make([]byte, length)
	n, err := rand.Read(buf)
	if err != nil {
		return "", err
	}

	if n != len(buf) {
		return "", errors.New("not enough random bytes read")
	}

	return hex.EncodeToString(buf), nil
}
