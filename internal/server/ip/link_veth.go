package ip

// Veth represents arguments for link of type veth.
type Veth struct {
	Link
	Peer Link
}

// Add adds new virtual link.
func (veth *Veth) Add() error {
	return veth.Link.add("veth", append([]string{"peer"}, veth.Peer.args()...))
}
