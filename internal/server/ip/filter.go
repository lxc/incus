package ip

import (
	"github.com/lxc/incus/v6/shared/subprocess"
)

// Action represents an action in filter.
type Action interface {
	AddAction() []string
}

// ActionPolice represents an action of 'police' type.
type ActionPolice struct {
	Rate  string
	Burst string
	Mtu   string
	Drop  bool
}

// AddAction generates a part of command specific for 'police' action.
func (a *ActionPolice) AddAction() []string {
	result := []string{"police"}
	if a.Rate != "" {
		result = append(result, "rate", a.Rate)
	}

	if a.Burst != "" {
		result = append(result, "burst", a.Burst)
	}

	if a.Mtu != "" {
		result = append(result, "mtu", a.Mtu)
	}

	if a.Drop {
		result = append(result, "drop")
	}

	return result
}

// Filter represents filter object.
type Filter struct {
	Dev      string
	Parent   string
	Protocol string
	Flowid   string
}

// U32Filter represents universal 32bit traffic control filter.
type U32Filter struct {
	Filter
	Value   string
	Mask    string
	Actions []Action
}

// Add adds universal 32bit traffic control filter to a node.
func (u32 *U32Filter) Add() error {
	cmd := []string{"filter", "add", "dev", u32.Dev}
	if u32.Parent != "" {
		cmd = append(cmd, "parent", u32.Parent)
	}

	cmd = append(cmd, "protocol", u32.Protocol)
	cmd = append(cmd, "u32", "match", "u32", u32.Value, u32.Mask)

	for _, action := range u32.Actions {
		actionCmd := action.AddAction()
		cmd = append(cmd, actionCmd...)
	}

	if u32.Flowid != "" {
		cmd = append(cmd, "flowid", u32.Flowid)
	}

	_, err := subprocess.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}
