package ip

// Vlan represents arguments for link of type vlan.
type Vlan struct {
	Link
	VlanID string
	Gvrp   bool
}

// additionalArgs generates vlan specific arguments.
func (vlan *Vlan) additionalArgs() []string {
	args := []string{"id", vlan.VlanID}
	if vlan.Gvrp {
		args = append(args, "gvrp", "on")
	}

	return args
}

// Add adds new virtual link.
func (vlan *Vlan) Add() error {
	// TODO: netlink library does not support vlan flags (necessary for gvrp)
	return vlan.Link.add("vlan", vlan.additionalArgs())
}
