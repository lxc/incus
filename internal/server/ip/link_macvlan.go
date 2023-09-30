package ip

// Macvlan represents arguments for link of type macvlan.
type Macvlan struct {
	Link
	Mode string
}

// Add adds new virtual link.
func (macvlan *Macvlan) Add() error {
	return macvlan.Link.add("macvlan", []string{"mode", macvlan.Mode})
}
