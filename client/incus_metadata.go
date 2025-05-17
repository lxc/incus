package incus

import (
	"errors"

	"github.com/lxc/incus/v6/shared/api"
)

// GetMetadataConfiguration returns a configuration metadata struct.
func (r *ProtocolIncus) GetMetadataConfiguration() (*api.MetadataConfiguration, error) {
	metadataConfiguration := api.MetadataConfiguration{}

	if !r.HasExtension("metadata_configuration") {
		return nil, errors.New("The server is missing the required \"metadata_configuration\" API extension")
	}

	_, err := r.queryStruct("GET", "/metadata/configuration", nil, "", &metadataConfiguration)
	if err != nil {
		return nil, err
	}

	return &metadataConfiguration, nil
}
