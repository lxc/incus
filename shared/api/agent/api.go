package api

// API10Put contains the fields which are needed for the incus-agent to connect to Incus.
type API10Put struct {
	// Context ID
	// Example: 2
	CID uint32 `json:"cid" yaml:"cid"`

	// Port of the vsock server
	// Example: 1234
	Port uint32 `json:"port" yaml:"port"`

	// Server certificate as PEM encoded X509
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Whether or not to enable /dev/incus
	// Example: true
	DevIncus bool `json:"dev_incus" yaml:"dev_incus"`
}
