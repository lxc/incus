package api

// Access represents everyone that may access a particular resource.
//
// swagger:model
//
// API extension: instance_access.
type Access []AccessEntry

// AccessEntry represents an entity having access to the resource.
//
// swagger:model
//
// API extension: instance_access.
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
