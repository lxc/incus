package ip

import (
	"github.com/lxc/incus/v6/shared/subprocess"
)

// Qdisc represents 'queueing discipline' object.
type Qdisc struct {
	Dev     string
	Handle  string
	Root    bool
	Ingress bool
}

func (qdisc *Qdisc) mainCmd() []string {
	cmd := []string{"qdisc", "add", "dev", qdisc.Dev}
	if qdisc.Handle != "" {
		cmd = append(cmd, "handle", qdisc.Handle)
	}

	if qdisc.Root {
		cmd = append(cmd, "root")
	}

	if qdisc.Ingress {
		cmd = append(cmd, "ingress")
	}

	return cmd
}

// Add adds qdisc to a node.
func (qdisc *Qdisc) Add() error {
	cmd := qdisc.mainCmd()
	_, err := subprocess.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// Delete deletes qdisc from node.
func (qdisc *Qdisc) Delete() error {
	cmd := []string{"qdisc", "del", "dev", qdisc.Dev}
	if qdisc.Root {
		cmd = append(cmd, "root")
	}

	if qdisc.Ingress {
		cmd = append(cmd, "ingress")
	}

	_, err := subprocess.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// QdiscHTB represents the hierarchy token bucket qdisc object.
type QdiscHTB struct {
	Qdisc
	Default string
}

// Add adds qdisc to a node.
func (qdisc *QdiscHTB) Add() error {
	cmd := qdisc.mainCmd()
	cmd = append(cmd, "htb")

	if qdisc.Default != "" {
		cmd = append(cmd, "default", qdisc.Default)
	}

	_, err := subprocess.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}
