package api

// access represents who has access to an instance or project
//
// swagger:model
//
// API extension: instances.
type Access []AccessEntry

type AccessEntry struct {
	// Certificate fingerprint
	// Example: 636b69519d27ae3b0e398cb7928043846ce1e3842f0ca7a589993dd913ab8cc9
	Identifier string `json:"identifier" yaml:"identifier"`

	// The role associated with the certificate
	// Example: admin, view, operator
	Role string `json:"role" yaml:"role"`

	// Which authorization method the certificate uses
	// Example: tls, openfga
	Provider string `json:"provider" yaml:"provider"`
}
