package api

import (
	"fmt"
)

// MetadataConfiguration represents a server's exposed configuration metadata
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfiguration struct {
	// Metadata about configuration keys.
	// Example: {'configs': {'instance': {...}}}
	Config MetadataConfig `json:"configs" yaml:"configs"`
}

// GetKeys provides an easy way to interact with MetadataConfiguration.
func (m *MetadataConfiguration) GetKeys(entity string, group string) (map[string]MetadataConfigKey, error) {
	keys := map[string]MetadataConfigKey{}

	// Get the entity.
	configEntity, ok := m.Config[MetadataConfigEntityName(entity)]
	if !ok {
		return nil, fmt.Errorf("Requested configuration entity %q doesn't exist", entity)
	}

	// Get the group.
	configGroup, ok := configEntity[MetadataConfigGroupName(group)]
	if !ok {
		return nil, fmt.Errorf("Requested configuration group %q doesn't exist", group)
	}

	// Go over the keys.
	for _, k := range configGroup.Keys {
		for name, entry := range k {
			keys[name] = entry
		}
	}

	return keys, nil
}

// MetadataConfig repreents metadata about configuration keys
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfig map[MetadataConfigEntityName]map[MetadataConfigGroupName]MetadataConfigGroup

// MetadataConfigEntityName represents a main API object type
// Example: instance
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfigEntityName string

// MetadataConfigGroupName represents the name of a group of config keys
// Example: volatile
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfigGroupName string

// MetadataConfigGroup represents a group of config keys
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfigGroup struct {
	Keys []map[string]MetadataConfigKey `json:"keys" yaml:"keys"`
}

// MetadataConfigKey describe a configuration key
//
// swagger:model
//
// API extension: metadata_configuration.
type MetadataConfigKey struct {
	// Condition specifies the condition that must be met for the option to be taken into account
	// Example: container
	Condition string `json:"condition,omitempty" yaml:"condition,omitempty"`

	// Scope defines if option apply to cluster or to the local server
	// Example: global
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`

	// Type specifies the type of the option
	// Example: string
	Type string `json:"type" yaml:"type"`

	// DefaultDesc specify default value for configuration
	// Example: "`DHCP on eth0`"
	Default string `json:"defaultdesc,omitempty" yaml:"defaultdesc,omitempty"`

	// LiveUpdate specifies whether the server must be restarted for the option to be updated
	// Example: "no"
	LiveUpdate string `json:"liveupdate,omitempty" yaml:"liveupdate,omitempty"`

	// ShortDesc provides short description for the configuration
	// Example: "Kernel modules to load before starting the instance"
	Description string `json:"shortdesc" yaml:"shortdesc"`

	// LongDesc provides long description for the option
	// Example: "Specify the kernel modules as a comma-separated list."
	LongDescription string `json:"longdesc" yaml:"longdesc"`
}
