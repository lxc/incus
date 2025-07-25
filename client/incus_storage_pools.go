package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// Storage pool handling functions

// GetStoragePoolNames returns the names of all storage pools.
func (r *ProtocolIncus) GetStoragePoolNames() ([]string, error) {
	if !r.HasExtension("storage") {
		return nil, errors.New("The server is missing the required \"storage\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/storage-pools"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetStoragePools returns a list of StoragePool entries.
func (r *ProtocolIncus) GetStoragePools() ([]api.StoragePool, error) {
	if !r.HasExtension("storage") {
		return nil, errors.New("The server is missing the required \"storage\" API extension")
	}

	pools := []api.StoragePool{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/storage-pools?recursion=1", nil, "", &pools)
	if err != nil {
		return nil, err
	}

	return pools, nil
}

// GetStoragePoolsWithFilter returns a filtered list of storage pools as StoragePool structs.
func (r *ProtocolIncus) GetStoragePoolsWithFilter(filters []string) ([]api.StoragePool, error) {
	if !r.HasExtension("storage") {
		return nil, errors.New("The server is missing the required \"storage\" API extension")
	}

	pools := []api.StoragePool{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))

	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools?%s", v.Encode()), nil, "", &pools)
	if err != nil {
		return nil, err
	}

	return pools, nil
}

// GetStoragePool returns a StoragePool entry for the provided pool name.
func (r *ProtocolIncus) GetStoragePool(name string) (*api.StoragePool, string, error) {
	if !r.HasExtension("storage") {
		return nil, "", errors.New("The server is missing the required \"storage\" API extension")
	}

	pool := api.StoragePool{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s", url.PathEscape(name)), nil, "", &pool)
	if err != nil {
		return nil, "", err
	}

	return &pool, etag, nil
}

// CreateStoragePool defines a new storage pool using the provided StoragePool struct.
func (r *ProtocolIncus) CreateStoragePool(pool api.StoragePoolsPost) error {
	if !r.HasExtension("storage") {
		return errors.New("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", "/storage-pools", pool, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateStoragePool updates the pool to match the provided StoragePool struct.
func (r *ProtocolIncus) UpdateStoragePool(name string, pool api.StoragePoolPut, ETag string) error {
	if !r.HasExtension("storage") {
		return errors.New("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/storage-pools/%s", url.PathEscape(name)), pool, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePool deletes a storage pool.
func (r *ProtocolIncus) DeleteStoragePool(name string) error {
	if !r.HasExtension("storage") {
		return errors.New("The server is missing the required \"storage\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/storage-pools/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetStoragePoolResources gets the resources available to a given storage pool.
func (r *ProtocolIncus) GetStoragePoolResources(name string) (*api.ResourcesStoragePool, error) {
	if !r.HasExtension("resources") {
		return nil, errors.New("The server is missing the required \"resources\" API extension")
	}

	res := api.ResourcesStoragePool{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/storage-pools/%s/resources", url.PathEscape(name)), nil, "", &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}
