package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	incus "github.com/lxc/incus/v7/client"
	clusterRequest "github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/shared/api"
)

func stagedMigrationRequest() api.InstancesPost {
	return api.InstancesPost{Name: "moving", InstancePut: api.InstancePut{Config: map[string]string{"volatile.uuid": uuid.NewString()}}, Source: api.InstanceSource{Type: "migration", Mode: "pull", Source: "moving", Live: true, Operation: "https://source:8443/1.0/operations/" + uuid.NewString()}}
}

func TestOVNNICMigrationActualEffectRouteNoLegacyFallback(t *testing.T) {
	for _, name := range []string{"old-handler", "stale-schema85-old-handler", "restarted-old-handler", "supported-handler", "ambiguous-target"} {
		t.Run(name, func(t *testing.T) {
			// Only the actual dispatch route determines support; advertisements have no role here.
			legacyEffects, stageEffects := 0, 0
			router := http.NewServeMux()
			router.HandleFunc("POST /1.0/instances", func(w http.ResponseWriter, r *http.Request) { legacyEffects++; w.WriteHeader(500) })
			if name == "supported-handler" || name == "ambiguous-target" {
				router.HandleFunc("POST /1.0/"+instanceOVNMigrationCmd.Path, func(w http.ResponseWriter, r *http.Request) {
					stageEffects++
					require.Equal(t, "moving", r.PathValue("name"))
					require.Equal(t, "original-project", r.URL.Query().Get("project"))
					require.Equal(t, "actual-target", r.URL.Query().Get("target"))
					if name == "ambiguous-target" {
						w.WriteHeader(500)
						return
					}

					require.NoError(t, json.NewEncoder(w).Encode(api.Response{Type: api.AsyncResponse, Status: "Operation created", StatusCode: 100, Metadata: json.RawMessage(`{"id":"target-operation","class":"task","status":"Running","status_code":103,"metadata":{}}`)}))
				})
			}

			server := httptest.NewTLSServer(router)
			t.Cleanup(server.Close)
			client, err := incus.ConnectIncus(server.URL, &incus.ConnectionArgs{InsecureSkipVerify: true, SkipGetServer: true, SkipGetEvents: true})
			require.NoError(t, err)
			req := stagedMigrationRequest()
			operation, err := ovnNICMigrationOperationFromURL(req.Source.Operation)
			require.NoError(t, err)
			op, err := ovnNICMigrationCreateTarget(client.UseProject("original-project").UseTarget("actual-target"), operation, req)
			require.Zero(t, legacyEffects)
			if name == "supported-handler" {
				require.NoError(t, err)
				require.NotNil(t, op)
				require.Equal(t, 1, stageEffects)
			} else {
				require.Error(t, err)
				require.Nil(t, op)
			}

			if name == "ambiguous-target" {
				require.Equal(t, 1, stageEffects)
			}
		})
	}
}

func TestOVNNICMigrationTargetHandlerAuthenticationBeforeEffects(t *testing.T) {
	for _, name := range []string{"client-spoof-notifier", "unix-spoof-notifier", "cluster-no-notifier", "wrong-name", "wrong-source", "stopped", "push", "wrong-operation", "missing-uuid", "valid"} {
		t.Run(name, func(t *testing.T) {
			req := stagedMigrationRequest()
			r := httptest.NewRequest(http.MethodPost, "/1.0/instances/moving/ovn-migration", nil)
			r.SetPathValue("name", "moving")
			r.Header.Set("User-Agent", clusterRequest.UserAgentNotifier)
			protocol := "cluster"
			switch name {
			case "client-spoof-notifier":
				protocol = "tls"
			case "unix-spoof-notifier":
				protocol = "unix"
			case "cluster-no-notifier":
				r.Header.Del("User-Agent")
			case "wrong-name":
				r.SetPathValue("name", "replacement")
			case "wrong-source":
				req.Source.Source = "other"
			case "stopped":
				req.Source.Live = false
			case "push":
				req.Source.Mode = "push"
			case "wrong-operation":
				req.Source.Operation = "https://source/other"
			case "missing-uuid":
				delete(req.Config, "volatile.uuid")
			}

			r = r.WithContext(context.WithValue(r.Context(), request.CtxProtocol, protocol))
			_, err := ovnNICMigrationTargetRequest(r, req)
			if name == "valid" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			// Nil daemon makes any access to target state/effects a failing test.
			response := instanceOVNMigrationPost(nil, r)
			recorder := httptest.NewRecorder()
			require.NoError(t, response.Render(recorder))
			require.NotEqual(t, http.StatusOK, recorder.Code)
		})
	}
}

func TestOVNNICMigrationTargetOriginalPayloadBinding(t *testing.T) {
	for _, name := range []string{"exact", "wrong-operation", "wrong-source-member", "wrong-uuid", "wrong-original-claim", "wrong-target-member", "duplicate-uuid", "lost-claim-before-ready"} {
		t.Run(name, func(t *testing.T) {
			cluster, closeCluster := db.NewTestCluster(t)
			t.Cleanup(closeCluster)
			req := stagedMigrationRequest()
			op, err := ovnNICMigrationOperationFromURL(req.Source.Operation)
			require.NoError(t, err)
			require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				source, err := tx.CreateNode("source", "source:8443")
				if err != nil {
					return err
				}

				other, err := tx.CreateNode("other", "other:8443")
				if err != nil {
					return err
				}

				result, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'moving',1,0,'',1)", source)
				if err != nil {
					return err
				}

				id, err := result.LastInsertId()
				if err != nil {
					return err
				}

				original := map[string]string{"host_name": "source-original", "last_state.ovn.host": `{"original":"exact-allocation"}`}
				for key, v := range original {
					req.Config["volatile.eth0."+key] = v
				}

				for key, v := range req.Config {
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, v)
					if err != nil {
						return err
					}
				}

				payload, err := json.Marshal(map[string]any{"InstanceID": id, "HostVolatile": original})
				if err != nil {
					return err
				}

				gen := uuid.NewString()
				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup(generation,source_node_id,instance_uuid,device_name,version,network_ids,payload) VALUES (?,?,?,'eth0',1,'[41]',?)", gen, source, req.Config["volatile.uuid"], string(payload))
				if err != nil {
					return err
				}

				target := cluster.GetNodeID()
				if name == "wrong-target-member" {
					target = other
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migrations(operation,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase) VALUES (?,1,?,?,?,?, 'authorized')", op, id, req.Config["volatile.uuid"], source, target)
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migration_devices(operation,device_name,generation) VALUES (?,'eth0',?)", op, gen)
				if err != nil {
					return err
				}

				switch name {
				case "wrong-operation":
					op = uuid.NewString()
				case "wrong-source-member":
					req.Source.Operation = "https://other:8443/1.0/operations/" + op
				case "wrong-uuid":
					req.Config["volatile.uuid"] = uuid.NewString()
				case "wrong-original-claim":
					req.Config["volatile.eth0.host_name"] = "replacement"
				case "duplicate-uuid":
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (999,'volatile.uuid',?)", req.Config["volatile.uuid"])
					if err != nil {
						result, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'duplicate',1,0,'',1)", source)
						if err != nil {
							return err
						}

						duplicate, err := result.LastInsertId()
						if err != nil {
							return err
						}

						_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", duplicate, req.Config["volatile.uuid"])
						if err != nil {
							return err
						}
					}

				case "lost-claim-before-ready":
					_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_nic_migration_devices SET target_volatile='{\"host_name\":\"uncertain-target\"}' WHERE operation=?", op)
					if err != nil {
						return err
					}
				}

				err = ovnNICMigrationValidateTarget(ctx, tx, op, "default", req)
				if name == "exact" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}

				var phase string
				require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT phase FROM networks_ovn_nic_migrations WHERE instance_id=?", id).Scan(&phase))
				require.Equal(t, "authorized", phase)
				return nil
			}))
		})
	}
}
