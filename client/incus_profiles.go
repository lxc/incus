package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// Profile handling functions

// GetProfileNames returns a list of available profile names.
func (r *ProtocolIncus) GetProfileNames() ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/profiles"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetProfiles returns a list of available Profile structs.
func (r *ProtocolIncus) GetProfiles() ([]api.Profile, error) {
	profiles := []api.Profile{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/profiles?recursion=1", nil, "", &profiles)
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// GetProfilesWithFilter returns a filtered list of available Profile structs.
func (r *ProtocolIncus) GetProfilesWithFilter(filters []string) ([]api.Profile, error) {
	profiles := []api.Profile{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))

	_, err := r.queryStruct("GET", fmt.Sprintf("/profiles?%s", v.Encode()), nil, "", &profiles)
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// GetProfilesAllProjects returns a list of profiles across all projects as Profile structs.
func (r *ProtocolIncus) GetProfilesAllProjects() ([]api.Profile, error) {
	err := r.CheckExtension("profiles_all_projects")
	if err != nil {
		return nil, errors.New(`The server is missing the required "profiles_all_projects" API extension`)
	}

	profiles := []api.Profile{}
	_, err = r.queryStruct("GET", "/profiles?recursion=1&all-projects=true", nil, "", &profiles)
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// GetProfilesAllProjectsWithFilter returns a filtered list of profiles across all projects as Profile structs.
func (r *ProtocolIncus) GetProfilesAllProjectsWithFilter(filters []string) ([]api.Profile, error) {
	err := r.CheckExtension("profiles_all_projects")
	if err != nil {
		return nil, errors.New(`The server is missing the required "profiles_all_projects" API extension`)
	}

	profiles := []api.Profile{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("all-projects", "true")
	v.Set("filter", parseFilters(filters))

	_, err = r.queryStruct("GET", fmt.Sprintf("/profiles?%s", v.Encode()), nil, "", &profiles)
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// GetProfile returns a Profile entry for the provided name.
func (r *ProtocolIncus) GetProfile(name string) (*api.Profile, string, error) {
	profile := api.Profile{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/profiles/%s", url.PathEscape(name)), nil, "", &profile)
	if err != nil {
		return nil, "", err
	}

	return &profile, etag, nil
}

// CreateProfile defines a new instance profile.
func (r *ProtocolIncus) CreateProfile(profile api.ProfilesPost) error {
	// Send the request
	_, _, err := r.query("POST", "/profiles", profile, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateProfile updates the profile to match the provided Profile struct.
func (r *ProtocolIncus) UpdateProfile(name string, profile api.ProfilePut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/profiles/%s", url.PathEscape(name)), profile, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameProfile renames an existing profile entry.
func (r *ProtocolIncus) RenameProfile(name string, profile api.ProfilePost) error {
	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/profiles/%s", url.PathEscape(name)), profile, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteProfile deletes a profile.
func (r *ProtocolIncus) DeleteProfile(name string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/profiles/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
