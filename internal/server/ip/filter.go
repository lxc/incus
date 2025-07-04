package ip

import (
	"fmt"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Action represents an action in filter.
type Action interface {
	toNetlink() (netlink.Action, error)
}

// ActionPolice represents an action of 'police' type.
type ActionPolice struct {
	Rate  uint32 // in byte/s
	Burst uint32 // in byte
	Mtu   uint32 // in byte
	Drop  bool
}

func (a *ActionPolice) toNetlink() (netlink.Action, error) {
	action := netlink.NewPoliceAction()

	action.Rate = a.Rate
	action.Burst = a.Burst
	action.Mtu = a.Mtu

	if a.Drop {
		action.ExceedAction = netlink.TC_POLICE_SHOT
	} else {
		action.ExceedAction = netlink.TC_POLICE_RECLASSIFY
	}

	return action, nil
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
	Value   uint32
	Mask    uint32
	Actions []Action
}

func parseProtocol(proto string) (uint16, error) {
	switch proto {
	case "all":
		return unix.ETH_P_ALL, nil
	default:
		return 0, fmt.Errorf("Unknown protocol %q", proto)
	}
}

// Add adds universal 32bit traffic control filter to a node.
func (u32 *U32Filter) Add() error {
	link, err := linkByName(u32.Dev)
	if err != nil {
		return err
	}

	proto, err := parseProtocol(u32.Protocol)
	if err != nil {
		return err
	}

	filter := &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: link.Attrs().Index,
			Protocol:  proto,
			Chain:     nil,
		},
		Sel: &netlink.TcU32Sel{
			Flags: netlink.TC_U32_TERMINAL,
			Nkeys: 1,
			Keys: []netlink.TcU32Key{
				{
					Mask: u32.Mask,
					Val:  u32.Value,
				},
			},
		},
	}

	for _, action := range u32.Actions {
		netlinkAction, err := action.toNetlink()
		if err != nil {
			return err
		}

		filter.Actions = append(filter.Actions, netlinkAction)
	}

	if u32.Parent != "" {
		parent, err := parseHandle(u32.Parent)
		if err != nil {
			return err
		}

		filter.Parent = parent
	}

	if u32.Flowid != "" {
		flowid, err := parseHandle(u32.Flowid)
		if err != nil {
			return err
		}

		filter.ClassId = flowid
	}

	err = netlink.FilterAdd(filter)
	if err != nil {
		return fmt.Errorf("Failed to add filter %v: %w", filter, err)
	}

	return nil
}
