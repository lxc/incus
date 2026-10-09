package ovn

import (
	"context"
	"errors"
	"fmt"
)

// NetworkReferenceInfrastructure identifies a current catalog network independently of NIC presence.
type NetworkReferenceInfrastructure struct {
	ProjectID int64
	NetworkID int64
	ParentID  int64
}

// RouterPort returns the constructor identity selected by the current numeric parent relationship.
func (n NetworkReferenceInfrastructure) RouterPort() string {
	if n.ParentID != 0 {
		return fmt.Sprintf("incus-net%d-lr-lrp-int-net%d", n.ParentID, n.NetworkID)
	}

	return fmt.Sprintf("incus-net%d-lr-lrp-int", n.NetworkID)
}

// NetworkACLPortGroupIDs reads native constructor candidates without granting deletion authority.
func (o *NB) NetworkACLPortGroupIDs(ctx context.Context, networkID int64) ([]int64, error) {
	if networkID <= 0 {
		return nil, errors.New("Invalid network ACL collection identity")
	}

	s, err := o.physicalReferenceSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	ids := []int64{}
	for _, row := range s.rows["Port_Group"] {
		name, ok := row["name"].(string)
		if !ok {
			return nil, errors.New("Network ACL collection candidate has an invalid name")
		}

		var aclID, candidateNetworkID int64
		_, err := fmt.Sscanf(name, "incus_acl%d_net%d", &aclID, &candidateNetworkID)
		if err != nil || aclID <= 0 || candidateNetworkID != networkID || name != fmt.Sprintf("incus_acl%d_net%d", aclID, networkID) {
			continue
		}

		ids = append(ids, aclID)
	}

	return ids, nil
}
