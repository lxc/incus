package ip

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// QdiscGeneric represents a qdisc identified only by its kind, using the kernel defaults for its options.
type QdiscGeneric struct {
	Qdisc
	Kind string
}

func (q *QdiscGeneric) netlinkQdisc() (*netlink.GenericQdisc, error) {
	attrs, err := q.netlinkAttrs()
	if err != nil {
		return nil, err
	}

	return &netlink.GenericQdisc{QdiscAttrs: attrs, QdiscType: q.Kind}, nil
}

// mapQdiscKindErr turns the kernel's report of an unregistered qdisc kind into a usable message.
func mapQdiscKindErr(err error) error {
	if errors.Is(err, unix.ENOENT) && strings.Contains(err.Error(), "Specified qdisc kind is unknown") {
		return errors.New("Not supported by the kernel")
	}

	return mapQdiscErr(err)
}

// Add adds a qdisc to a device.
func (q *QdiscGeneric) Add() error {
	qdisc, err := q.netlinkQdisc()
	if err != nil {
		return err
	}

	err = netlink.QdiscAdd(qdisc)
	if err != nil {
		return fmt.Errorf("Failed to add qdisc %q: %w", q.Kind, mapQdiscKindErr(err))
	}

	return nil
}

// Delete deletes a qdisc from a device.
func (q *QdiscGeneric) Delete() error {
	qdisc, err := q.netlinkQdisc()
	if err != nil {
		return err
	}

	err = netlink.QdiscDel(qdisc)
	if err != nil {
		return fmt.Errorf("Failed to delete qdisc %q: %w", q.Kind, mapQdiscErr(err))
	}

	return nil
}
