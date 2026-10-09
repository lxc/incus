//go:build linux && cgo && !agent

package db

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/shared/api"
)

// ValidateInstanceOVNConfigUpdate prevents user updates from borrowing another instance's identity or host allocation.
func (c *ClusterTx) ValidateInstanceOVNConfigUpdate(ctx context.Context, instanceID int, next map[string]string) error {
	var currentUUID string
	err := c.tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=i.id AND key='volatile.uuid'),'') FROM instances i WHERE i.id=?", instanceID).Scan(&currentUUID)
	if err != nil {
		return err
	}

	identity := next["volatile.uuid"]
	if identity != "" && identity != currentUUID {
		var borrowed bool
		err = c.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM instances_config WHERE key='volatile.uuid' AND value=? AND instance_id<>?)", identity, instanceID).Scan(&borrowed)
		if err != nil {
			return err
		}

		if borrowed {
			return api.StatusErrorf(http.StatusConflict, "Instance UUID is already held by another instance")
		}
	}

	keys := make([]string, 0, len(next))
	for key, value := range next {
		if value != "" && strings.HasPrefix(key, "volatile.") && (strings.HasSuffix(key, ".last_state.ovn.host") || strings.HasSuffix(key, ".last_state.ovn.physical")) {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)
	for _, key := range keys {
		var current string
		err = c.tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key=?),'')", instanceID, key).Scan(&current)
		if err != nil {
			return err
		}

		if next[key] != current {
			return api.StatusErrorf(http.StatusConflict, "OVN NIC host allocation %q cannot be replaced by an instance update", key)
		}
	}

	return nil
}
