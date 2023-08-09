package ip

// Gretap represents arguments for link of type gretap.
type Gretap struct {
	Link
	Local  string
	Remote string
}

// additionalArgs generates gretap specific arguments.
func (g *Gretap) additionalArgs() []string {
	return []string{"local", g.Local, "remote", g.Remote}
}

// Add adds new virtual link.
func (g *Gretap) Add() error {
	return g.Link.add("gretap", g.additionalArgs())
}
