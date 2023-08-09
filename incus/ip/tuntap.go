package ip

import (
	"github.com/lxc/incus/shared"
)

// Tuntap represents arguments for tuntap manipulation.
type Tuntap struct {
	Name       string
	Mode       string
	MultiQueue bool
}

// Add adds new tuntap interface.
func (t *Tuntap) Add() error {
	cmd := []string{"tuntap", "add", "name", t.Name, "mode", t.Mode}
	if t.MultiQueue {
		cmd = append(cmd, "multi_queue")
	}

	_, err := shared.RunCommand("ip", cmd...)
	if err != nil {
		return err
	}

	return nil
}
