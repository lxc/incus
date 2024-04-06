package incus

import (
	"fmt"

	"github.com/lxc/incus/v6/shared/api"
)

// GetCluster returns information about a cluster.
func (r *ProtocolIncus) GetCluster() (*api.Cluster, string, error) {
	if !r.HasExtension("clustering") {
		return nil, "", fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	cluster := &api.Cluster{}
	etag, err := r.queryStruct("GET", "/cluster", nil, "", &cluster)
	if err != nil {
		return nil, "", err
	}

	return cluster, etag, nil
}

// UpdateCluster requests to bootstrap a new cluster or join an existing one.
func (r *ProtocolIncus) UpdateCluster(cluster api.ClusterPut, ETag string) (Operation, error) {
	if !r.HasExtension("clustering") {
		return nil, fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	if cluster.ServerAddress != "" || cluster.ClusterToken != "" || len(cluster.MemberConfig) > 0 {
		if !r.HasExtension("clustering_join") {
			return nil, fmt.Errorf("The server is missing the required \"clustering_join\" API extension")
		}
	}

	op, _, err := r.queryOperation("PUT", "/cluster", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteClusterMember makes the given member leave the cluster (gracefully or not,
// depending on the force flag).
func (r *ProtocolIncus) DeleteClusterMember(name string, force bool) error {
	if !r.HasExtension("clustering") {
		return fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	params := ""
	if force {
		params += "?force=1"
	}

	_, _, err := r.query("DELETE", fmt.Sprintf("/cluster/members/%s%s", name, params), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetClusterMemberNames returns the URLs of the current members in the cluster.
func (r *ProtocolIncus) GetClusterMemberNames() ([]string, error) {
	if !r.HasExtension("clustering") {
		return nil, fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/cluster/members"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetClusterMembers returns the current members of the cluster.
func (r *ProtocolIncus) GetClusterMembers() ([]api.ClusterMember, error) {
	if !r.HasExtension("clustering") {
		return nil, fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	members := []api.ClusterMember{}
	_, err := r.queryStruct("GET", "/cluster/members?recursion=1", nil, "", &members)
	if err != nil {
		return nil, err
	}

	return members, nil
}

// GetClusterMember returns information about the given member.
func (r *ProtocolIncus) GetClusterMember(name string) (*api.ClusterMember, string, error) {
	if !r.HasExtension("clustering") {
		return nil, "", fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	member := api.ClusterMember{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("/cluster/members/%s", name), nil, "", &member)
	if err != nil {
		return nil, "", err
	}

	return &member, etag, nil
}

// UpdateClusterMember updates information about the given member.
func (r *ProtocolIncus) UpdateClusterMember(name string, member api.ClusterMemberPut, ETag string) error {
	if !r.HasExtension("clustering_edit_roles") {
		return fmt.Errorf("The server is missing the required \"clustering_edit_roles\" API extension")
	}

	if member.FailureDomain != "" {
		if !r.HasExtension("clustering_failure_domains") {
			return fmt.Errorf("The server is missing the required \"clustering_failure_domains\" API extension")
		}
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/cluster/members/%s", name), member, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameClusterMember changes the name of an existing member.
func (r *ProtocolIncus) RenameClusterMember(name string, member api.ClusterMemberPost) error {
	if !r.HasExtension("clustering") {
		return fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	_, _, err := r.query("POST", fmt.Sprintf("/cluster/members/%s", name), member, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateClusterMember generates a join token to add a cluster member.
func (r *ProtocolIncus) CreateClusterMember(member api.ClusterMembersPost) (Operation, error) {
	if !r.HasExtension("clustering_join_token") {
		return nil, fmt.Errorf("The server is missing the required \"clustering_join_token\" API extension")
	}

	op, _, err := r.queryOperation("POST", "/cluster/members", member, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateClusterCertificate updates the cluster certificate for every node in the cluster.
func (r *ProtocolIncus) UpdateClusterCertificate(certs api.ClusterCertificatePut, ETag string) error {
	if !r.HasExtension("clustering_update_cert") {
		return fmt.Errorf("The server is missing the required \"clustering_update_cert\" API extension")
	}

	_, _, err := r.query("PUT", "/cluster/certificate", certs, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterMemberState gets state information about a cluster member.
func (r *ProtocolIncus) GetClusterMemberState(name string) (*api.ClusterMemberState, string, error) {
	err := r.CheckExtension("cluster_member_state")
	if err != nil {
		return nil, "", err
	}

	state := api.ClusterMemberState{}
	u := api.NewURL().Path("cluster", "members", name, "state")
	etag, err := r.queryStruct("GET", u.String(), nil, "", &state)
	if err != nil {
		return nil, "", err
	}

	return &state, etag, err
}

// UpdateClusterMemberState evacuates or restores a cluster member.
func (r *ProtocolIncus) UpdateClusterMemberState(name string, state api.ClusterMemberStatePost) (Operation, error) {
	if !r.HasExtension("clustering_evacuation") {
		return nil, fmt.Errorf("The server is missing the required \"clustering_evacuation\" API extension")
	}

	op, _, err := r.queryOperation("POST", fmt.Sprintf("/cluster/members/%s/state", name), state, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetClusterGroups returns the cluster groups.
func (r *ProtocolIncus) GetClusterGroups() ([]api.ClusterGroup, error) {
	if !r.HasExtension("clustering_groups") {
		return nil, fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	groups := []api.ClusterGroup{}

	_, err := r.queryStruct("GET", "/cluster/groups?recursion=1", nil, "", &groups)
	if err != nil {
		return nil, err
	}

	return groups, nil
}

// GetClusterGroupNames returns the cluster group names.
func (r *ProtocolIncus) GetClusterGroupNames() ([]string, error) {
	if !r.HasExtension("clustering_groups") {
		return nil, fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	urls := []string{}

	_, err := r.queryStruct("GET", "/cluster/groups", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames("/1.0/cluster/groups", urls...)
}

// RenameClusterGroup changes the name of an existing cluster group.
func (r *ProtocolIncus) RenameClusterGroup(name string, group api.ClusterGroupPost) error {
	if !r.HasExtension("clustering_groups") {
		return fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	_, _, err := r.query("POST", fmt.Sprintf("/cluster/groups/%s", name), group, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateClusterGroup creates a new cluster group.
func (r *ProtocolIncus) CreateClusterGroup(group api.ClusterGroupsPost) error {
	if !r.HasExtension("clustering_groups") {
		return fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	_, _, err := r.query("POST", "/cluster/groups", group, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteClusterGroup deletes an existing cluster group.
func (r *ProtocolIncus) DeleteClusterGroup(name string) error {
	if !r.HasExtension("clustering_groups") {
		return fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	_, _, err := r.query("DELETE", fmt.Sprintf("/cluster/groups/%s", name), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateClusterGroup updates information about the given cluster group.
func (r *ProtocolIncus) UpdateClusterGroup(name string, group api.ClusterGroupPut, ETag string) error {
	if !r.HasExtension("clustering_groups") {
		return fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/cluster/groups/%s", name), group, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterGroup returns information about the given cluster group.
func (r *ProtocolIncus) GetClusterGroup(name string) (*api.ClusterGroup, string, error) {
	if !r.HasExtension("clustering_groups") {
		return nil, "", fmt.Errorf("The server is missing the required \"clustering_groups\" API extension")
	}

	group := api.ClusterGroup{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("/cluster/groups/%s", name), nil, "", &group)
	if err != nil {
		return nil, "", err
	}

	return &group, etag, nil
}
