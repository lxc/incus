package ip

import (
	"errors"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Qdisc represents 'queueing discipline' object.
type Qdisc struct {
	Dev    string
	Handle string
	Parent string
}

func (q *Qdisc) netlinkAttrs() (netlink.QdiscAttrs, error) {
	link, err := linkByName(q.Dev)
	if err != nil {
		return netlink.QdiscAttrs{}, err
	}

	var handle uint32
	if q.Handle != "" {
		handle, err = parseHandle(q.Handle)
		if err != nil {
			return netlink.QdiscAttrs{}, err
		}
	}

	var parent uint32
	if q.Parent != "" {
		parent, err = parseHandle(q.Parent)
		if err != nil {
			return netlink.QdiscAttrs{}, err
		}
	}

	return netlink.QdiscAttrs{
		LinkIndex: link.Attrs().Index,
		Handle:    handle,
		Parent:    parent,
	}, nil
}

func mapQdiscErr(err error) error {
	if errors.Is(err, unix.EINVAL) && strings.Contains(err.Error(), "Invalid handle") {
		return unix.ENOENT
	}

	return err
}
