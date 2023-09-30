package config

// ProxyAddress represents a proxy address configuration.
type ProxyAddress struct {
	ConnType string
	Abstract bool
	Address  string
	Ports    []uint64
}
