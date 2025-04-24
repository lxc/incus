package ip

import (
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
)

// QdiscIngress represents an ingress qdisc object.
type QdiscIngress struct {
	Qdisc
}

// Add adds an ingress qdisc to a device.
func (q *QdiscIngress) Add() error {
	attrs, err := q.netlinkAttrs()
	if err != nil {
		return err
	}

	if q.Parent != "" {
		return errors.New("Ingress qdisc cannot have parent")
	}

	attrs.Parent = netlink.HANDLE_INGRESS

	ingress := &netlink.Ingress{
		QdiscAttrs: attrs,
	}

	err = netlink.QdiscAdd(ingress)
	if err != nil {
		return fmt.Errorf("Failed to add ingress qdisc %v: %w", ingress, mapQdiscErr(err))
	}

	return nil
}

// Delete deletes an ingress qdisc from a device.
func (q *QdiscIngress) Delete() error {
	attrs, err := q.netlinkAttrs()
	if err != nil {
		return err
	}

	attrs.Parent = netlink.HANDLE_INGRESS

	ingress := &netlink.Ingress{
		QdiscAttrs: attrs,
	}

	err = netlink.QdiscDel(ingress)
	if err != nil {
		return fmt.Errorf("Failed to delete ingress qdisc %v: %w", ingress, mapQdiscErr(err))
	}

	return nil
}
