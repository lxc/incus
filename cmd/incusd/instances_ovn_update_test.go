//go:build linux && cgo && !agent

package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cancel"
)

type ovnUpdateAdmissionInstance struct {
	instance.Instance
	id     int
	config map[string]string
}

func (i *ovnUpdateAdmissionInstance) ID() int                        { return i.id }
func (i *ovnUpdateAdmissionInstance) LocalConfig() map[string]string { return i.config }
func (i *ovnUpdateAdmissionInstance) ETag() []any                    { return nil }
func (i *ovnUpdateAdmissionInstance) Architecture() int              { return 1 }
func (i *ovnUpdateAdmissionInstance) Description() string            { return "" }
func (i *ovnUpdateAdmissionInstance) IsEphemeral() bool              { return false }
func (i *ovnUpdateAdmissionInstance) Profiles() []api.Profile        { return nil }
func (i *ovnUpdateAdmissionInstance) LocalDevices() deviceConfig.Devices {
	return deviceConfig.Devices{}
}

func TestOVNNICUserUpdateHandlersRefuseBorrowing(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	sourceUUID, targetUUID := uuid.NewString(), uuid.NewString()
	stored := map[string]string{"volatile.uuid": targetUUID, "user.keep": "target"}
	var sourceID, targetID int
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, item := range []struct {
			name, identity string
			id             *int
		}{
			{"source", sourceUUID, &sourceID},
			{"target", targetUUID, &targetID},
		} {
			res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,?,1,0,'',1)", tx.GetNodeID(), item.name)
			if err != nil {
				return err
			}

			id, err := res.LastInsertId()
			if err != nil {
				return err
			}

			*item.id = int(id)
			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", id, item.identity)
			if err != nil {
				return err
			}
		}

		return nil
	}))

	previous := instance.Load
	instance.Load = func(_ *state.State, args db.InstanceArgs, _ api.Project) (instance.Instance, error) {
		require.Equal(t, "target", args.Name)
		// Other methods are nil: reaching Update or operation effects is a test failure.
		return &ovnUpdateAdmissionInstance{id: targetID, config: maps.Clone(stored)}, nil
	}

	t.Cleanup(func() { instance.Load = previous })
	d := &Daemon{db: &db.DB{Cluster: cluster}, shutdownCtx: ctx, waitReady: cancel.New(ctx)}
	d.waitReady.Cancel()
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		for _, field := range []string{"volatile.uuid", "volatile.eth0.last_state.ovn.host", "volatile.eth0.last_state.ovn.physical"} {
			t.Run(method+"/"+field, func(t *testing.T) {
				next := maps.Clone(stored)
				next[field] = sourceUUID
				body, err := json.Marshal(api.InstancePut{Config: next})
				require.NoError(t, err)
				r := httptest.NewRequest(method, "/1.0/instances/target", strings.NewReader(string(body)))
				r.SetPathValue("name", "target")
				r = r.WithContext(context.WithValue(r.Context(), request.CtxProtocol, "cluster"))
				var result response.Response
				if method == http.MethodPut {
					result = instancePut(d, r)
				} else {
					result = instancePatch(d, r)
				}

				w := httptest.NewRecorder()
				require.NoError(t, result.Render(w))
				require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
				require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					for id, identity := range map[int]string{sourceID: sourceUUID, targetID: targetUUID} {
						var current string
						err := tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.uuid'", id).Scan(&current)
						require.Equal(t, identity, current)
						if err != nil {
							return err
						}
					}

					return nil
				}))
			})
		}
	}
}
