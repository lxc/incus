package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"
)

func nicPortCleanupFixture() (*NB, *nicRouteCleanupClient, string) {
	root, sw, port := uuid.NewString(), uuid.NewString(), uuid.NewString()
	client := &nicRouteCleanupClient{tables: map[string][]ovsdb.Row{
		"NB_Global":           {{"_uuid": ovsdb.UUID{GoUUID: root}}},
		"Logical_Switch":      {{"_uuid": ovsdb.UUID{GoUUID: sw}, "name": "incus-net42-ls-int", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}}},
		"Logical_Switch_Port": {{"_uuid": ovsdb.UUID{GoUUID: port}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "instance-original", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"incus_location": "source", "foreign": "preserved"}}, "enabled": ovsdb.OvsSet{GoSet: []any{true}}}},
	}}
	return &NB{client: client, backendID: root}, client, port
}

func captureNICPortTest(t *testing.T, nb *NB) NICPortCleanup {
	t.Helper()
	plan, err := nb.CaptureNICPortCleanup(context.Background(), "incus-net42-ls-int", "instance-original", "source")
	require.NoError(t, err)
	return plan
}

func TestNICPortCleanupStoredReplay(t *testing.T) {
	nb, c, _ := nicPortCleanupFixture()
	plan := captureNICPortTest(t, nb)
	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	var stored NICPortCleanup
	require.NoError(t, json.Unmarshal(wire, &stored))
	fresh := &NB{client: c, backendID: nb.backendID}
	oldVersion := c.tables["Logical_Switch_Port"][0]["_version"]
	require.NoError(t, fresh.ApplyNICPortCleanup(context.Background(), stored))
	enabled, err := nicCleanupPortEnabled(c.tables["Logical_Switch_Port"][0]["enabled"])
	require.NoError(t, err)
	require.False(t, enabled)
	require.NotEqual(t, oldVersion, c.tables["Logical_Switch_Port"][0]["_version"])
	require.NoError(t, fresh.ApplyNICPortCleanup(context.Background(), stored))
	external, err := nicCleanupStringMap(c.tables["Logical_Switch_Port"][0]["external_ids"])
	require.NoError(t, err)
	require.Equal(t, "preserved", external["foreign"])
}

func TestNICPortCleanupCaptureRefusals(t *testing.T) {
	for _, name := range []string{"foreign-source", "foreign-reference", "absent-port", "ambiguous-port"} {
		t.Run(name, func(t *testing.T) {
			nb, c, port := nicPortCleanupFixture()
			switch name {
			case "foreign-source":
				c.tables["Logical_Switch_Port"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"incus_location": "target"}}
			case "foreign-reference":
				c.tables["Logical_Switch"] = append(c.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}})
			case "absent-port":
				c.tables["Logical_Switch_Port"] = nil
			case "ambiguous-port":
				duplicate := ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "_version": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "instance-original", "external_ids": ovsdb.OvsMap{GoMap: map[any]any{"incus_location": "source"}}, "enabled": true}
				c.tables["Logical_Switch_Port"] = append(c.tables["Logical_Switch_Port"], duplicate)
			}

			_, err := nb.CaptureNICPortCleanup(context.Background(), "incus-net42-ls-int", "instance-original", "source")
			require.Error(t, err)
			for _, ops := range c.calls {
				for _, op := range ops {
					require.NotEqual(t, ovsdb.OperationUpdate, op.Op)
				}
			}
		})
	}
}

func TestNICPortCleanupReplayRefusals(t *testing.T) {
	for _, name := range []string{"root-replaced", "source-transferred", "renamed-port", "switch-replaced", "foreign-reference", "stale-client"} {
		t.Run(name, func(t *testing.T) {
			nb, c, port := nicPortCleanupFixture()
			plan := captureNICPortTest(t, nb)
			switch name {
			case "root-replaced":
				c.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "source-transferred":
				c.tables["Logical_Switch_Port"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"incus_location": "target"}}
			case "renamed-port":
				c.tables["Logical_Switch_Port"][0]["name"] = "foreign-name"
			case "switch-replaced":
				c.tables["Logical_Switch"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "foreign-reference":
				c.tables["Logical_Switch"] = append(c.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}})
			case "stale-client":
				nb = &NB{client: c, backendID: uuid.NewString()}
			}

			require.Error(t, nb.ApplyNICPortCleanup(context.Background(), plan))
			enabled, err := nicCleanupPortEnabled(c.tables["Logical_Switch_Port"][0]["enabled"])
			require.NoError(t, err)
			require.True(t, enabled)
		})
	}
}

// OVN rewrites the original row's "up" status after the source interface detaches.
func TestNICPortCleanupStatusRewriteAfterCapture(t *testing.T) {
	nb, c, _ := nicPortCleanupFixture()
	plan := captureNICPortTest(t, nb)
	c.tables["Logical_Switch_Port"][0]["up"] = ovsdb.OvsSet{GoSet: []any{false}}
	c.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	require.NoError(t, nb.VerifyNICPortCleanup(context.Background(), plan))
	require.NoError(t, nb.ApplyNICPortCleanup(context.Background(), plan))
	enabled, err := nicCleanupPortEnabled(c.tables["Logical_Switch_Port"][0]["enabled"])
	require.NoError(t, err)
	require.False(t, enabled)
	external, err := nicCleanupStringMap(c.tables["Logical_Switch_Port"][0]["external_ids"])
	require.NoError(t, err)
	require.Equal(t, "preserved", external["foreign"])
	require.NoError(t, nb.ApplyNICPortCleanup(context.Background(), plan))
}

func TestNICPortCleanupGuardAtDispatch(t *testing.T) {
	nb, c, _ := nicPortCleanupFixture()
	plan := captureNICPortTest(t, nb)
	c.beforeWrite = func() {
		c.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
		c.tables["Logical_Switch_Port"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"incus_location": "target"}}
	}

	require.Error(t, nb.ApplyNICPortCleanup(context.Background(), plan))
	enabled, err := nicCleanupPortEnabled(c.tables["Logical_Switch_Port"][0]["enabled"])
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestNICPortCleanupLostAcknowledgment(t *testing.T) {
	nb, c, _ := nicPortCleanupFixture()
	plan := captureNICPortTest(t, nb)
	c.afterWriteError = errors.New("inert lost reply after committed disable")
	require.Error(t, nb.ApplyNICPortCleanup(context.Background(), plan))
	enabled, err := nicCleanupPortEnabled(c.tables["Logical_Switch_Port"][0]["enabled"])
	require.NoError(t, err)
	require.False(t, enabled)
	require.NoError(t, (&NB{client: c, backendID: nb.backendID}).ApplyNICPortCleanup(context.Background(), plan))
}

func TestNICPortCleanupAbsencePreservesReplacement(t *testing.T) {
	nb, c, _ := nicPortCleanupFixture()
	plan := captureNICPortTest(t, nb)
	replacement := uuid.NewString()
	c.tables["Logical_Switch_Port"][0]["_uuid"] = ovsdb.UUID{GoUUID: replacement}
	c.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
	c.tables["Logical_Switch"][0]["ports"] = ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: replacement}}}
	require.NoError(t, nb.ApplyNICPortCleanup(context.Background(), plan))
	enabled, err := nicCleanupPortEnabled(c.tables["Logical_Switch_Port"][0]["enabled"])
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, replacement, c.tables["Logical_Switch_Port"][0]["_uuid"].(ovsdb.UUID).GoUUID)
}

func TestNICPortCleanupReadOnlyPreflight(t *testing.T) {
	for _, mode := range []string{"original", "disabled-replay", "guarded-absence", "root-replaced", "source-transferred", "status-version-changed", "switch-replaced", "foreign-reference"} {
		t.Run(mode, func(t *testing.T) {
			nb, client, port := nicPortCleanupFixture()
			plan := captureNICPortTest(t, nb)
			switch mode {
			case "disabled-replay":
				require.NoError(t, nb.ApplyNICPortCleanup(context.Background(), plan))
			case "guarded-absence":
				client.tables["Logical_Switch_Port"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				client.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
				client.tables["Logical_Switch"][0]["ports"] = ovsdb.OvsSet{}
			case "root-replaced":
				client.tables["NB_Global"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "source-transferred":
				client.tables["Logical_Switch_Port"][0]["external_ids"] = ovsdb.OvsMap{GoMap: map[any]any{"incus_location": "target"}}
			case "status-version-changed":
				client.tables["Logical_Switch_Port"][0]["_version"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "switch-replaced":
				client.tables["Logical_Switch"][0]["_uuid"] = ovsdb.UUID{GoUUID: uuid.NewString()}
			case "foreign-reference":
				client.tables["Logical_Switch"] = append(client.tables["Logical_Switch"], ovsdb.Row{"_uuid": ovsdb.UUID{GoUUID: uuid.NewString()}, "name": "foreign", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: port}}}})
			}

			before := make(map[string][]ovsdb.Row)
			for table, rows := range client.tables {
				for _, row := range rows {
					normalized := ovsdb.Row{}
					for key, value := range row {
						normalized[key] = nicRouteSetStrings(value)
					}

					before[table] = append(before[table], normalized)
				}
			}

			client.calls = nil
			err := nb.VerifyNICPortCleanup(context.Background(), plan)
			if mode == "original" || mode == "disabled-replay" || mode == "guarded-absence" || mode == "status-version-changed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			for table, rows := range client.tables {
				for index, row := range rows {
					normalized := ovsdb.Row{}
					for key, value := range row {
						normalized[key] = nicRouteSetStrings(value)
					}

					require.Equal(t, before[table][index], normalized)
				}
			}

			for _, transaction := range client.calls {
				for _, op := range transaction {
					require.Contains(t, []string{ovsdb.OperationSelect, ovsdb.OperationWait}, op.Op)
				}
			}
		})
	}
}
