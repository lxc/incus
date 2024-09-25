package ip

// Dummy represents arguments for link device of type dummy.
type Dummy struct {
	Link
}

// Add adds new virtual link.
func (d *Dummy) Add() error {
	return d.Link.add("dummy", nil)
}
