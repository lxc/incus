package ip

// Bridge represents arguments for link device of type bridge.
type Bridge struct {
	Link
}

// Add adds new virtual link.
func (b *Bridge) Add() error {
	return b.Link.add("bridge", nil)
}
