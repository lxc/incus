//go:build !linux || !cgo

package endpoints

import (
	"errors"
	"net"
)

func localCreateListener(path string, group string) (net.Listener, error) {
	return nil, errors.New("Platform isn't supported")
}

func createDevIncuslListener(path string) (net.Listener, error) {
	return nil, errors.New("Platform isn't supported")
}
