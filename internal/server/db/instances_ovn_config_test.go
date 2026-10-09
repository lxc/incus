//go:build linux && cgo && !agent

package db

import (
	"context"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/shared/api"
)

func TestInstanceOVNConfigUserUpdateAdmission(t *testing.T) {
	for _, mode := range []string{"unchanged", "unset-claims", "unset-uuid", "omit-uuid", "new-uuid", "borrowed-uuid", "borrowed-host", "borrowed-physical", "new-device-claim", "unchanged-duplicate-uuid"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			identities := []string{uuid.NewString(), uuid.NewString()}
			ids := []int{}
			stored := map[string]string{"volatile.uuid": identities[1], "volatile.eth0.last_state.ovn.host": "own-host", "volatile.eth0.last_state.ovn.physical": "own-physical", "user.keep": "target"}
			for i, name := range []string{"source", "copy"} {
				res, err := tx.tx.ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,?,1,?,'',1)", tx.nodeID, name, i)
				require.NoError(t, err)
				id, err := res.LastInsertId()
				require.NoError(t, err)
				ids = append(ids, int(id))
				config := map[string]string{"volatile.uuid": identities[i], "volatile.eth0.last_state.ovn.host": "source-host"}
				if i == 1 {
					config = stored
				}

				for key, value := range config {
					_, err = tx.tx.ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
					require.NoError(t, err)
				}
			}

			next := maps.Clone(stored)
			next["user.keep"] = "updated"
			switch mode {
			case "unset-claims":
				next["volatile.eth0.last_state.ovn.host"] = ""
				delete(next, "volatile.eth0.last_state.ovn.physical")
			case "unset-uuid":
				next["volatile.uuid"] = ""
			case "omit-uuid":
				delete(next, "volatile.uuid")
			case "new-uuid":
				next["volatile.uuid"] = uuid.NewString()
			case "borrowed-uuid":
				next["volatile.uuid"] = identities[0]
			case "borrowed-host":
				next["volatile.eth0.last_state.ovn.host"] = "source-host"
			case "borrowed-physical":
				next["volatile.eth0.last_state.ovn.physical"] = "source-physical"
			case "new-device-claim":
				next["volatile.other.last_state.ovn.host"] = "source-host"
			case "unchanged-duplicate-uuid":
				_, err := tx.tx.ExecContext(ctx, "UPDATE instances_config SET value=? WHERE instance_id=? AND key='volatile.uuid'", identities[0], ids[1])
				require.NoError(t, err)
				stored["volatile.uuid"], next["volatile.uuid"] = identities[0], identities[0]
			}

			request := maps.Clone(next)
			err := tx.ValidateInstanceOVNConfigUpdate(ctx, ids[1], next)
			refused := mode == "borrowed-uuid" || mode == "borrowed-host" || mode == "borrowed-physical" || mode == "new-device-claim"
			if refused {
				require.True(t, api.StatusErrorCheck(err, 409), "%v", err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, request, next, "admission cannot silently normalize a user request")
			for key, value := range stored {
				var current string
				require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key=?", ids[1], key).Scan(&current))
				require.Equal(t, value, current)
			}

			var sourceUUID, sourceClaim string
			require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.uuid'", ids[0]).Scan(&sourceUUID))
			require.NoError(t, tx.tx.QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.ovn.host'", ids[0]).Scan(&sourceClaim))
			require.Equal(t, identities[0], sourceUUID)
			require.Equal(t, "source-host", sourceClaim)
		})
	}
}
