package main

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
	"github.com/lxc/incus/v7/internal/server/systemd_report"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"
)

func readSystemdReportMetrics(t *testing.T, path string) map[string]float64 {
	t.Helper()

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = conn.Write([]byte(`{"method":"io.systemd.Metrics.List","more":true}` + "\x00"))
	require.NoError(t, err)

	reader := bufio.NewReader(conn)
	values := make(map[string]float64)

	for {
		data, err := reader.ReadBytes(0)
		require.NoError(t, err)

		var reply struct {
			Parameters struct {
				Name   string            `json:"name"`
				Fields map[string]string `json:"fields"`
				Value  float64           `json:"value"`
			} `json:"parameters"`
			Continues bool   `json:"continues"`
			Error     string `json:"error"`
		}

		require.NoError(t, json.Unmarshal(data[:len(data)-1], &reply))
		require.Empty(t, reply.Error)

		key := reply.Parameters.Name
		if key == "org.linuxcontainers.Incus.unresolvedWarnings" {
			require.Empty(t, reply.Parameters.Fields)
		}

		if reply.Parameters.Fields["type"] != "" {
			key += "/" + reply.Parameters.Fields["type"]
		}

		_, exists := values[key]
		require.False(t, exists, "Duplicate metric %q", key)

		values[key] = reply.Parameters.Value

		if !reply.Continues {
			return values
		}
	}
}

func createSystemdReportWarning(t *testing.T, client incus.InstanceServer, location string, message string) {
	t.Helper()

	_, _, err := client.RawQuery("POST", "/internal/debug/warnings", map[string]any{
		"location": location, "type_code": warningtype.Undefined, "entity_type_code": -1, "entity_id": -1, "message": message,
	}, "")
	require.NoError(t, err)
}

func systemdReportWarningUUID(t *testing.T, client incus.InstanceServer, message string) string {
	t.Helper()

	warnings, err := client.GetWarnings()
	require.NoError(t, err)

	for _, warning := range warnings {
		if warning.LastMessage == message {
			return warning.UUID
		}
	}

	t.Fatalf("Warning %q not found", message)

	return ""
}

func TestIntegration_SystemdReportNodeWarnings(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	t.Cleanup(cleanup)

	incusClient, err := incus.ConnectIncusUnix(daemon.os.GetUnixSocket(), nil)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	source, err := systemd_report.Start(path, daemon.systemdReportSnapshot)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })

	prefix := "org.linuxcontainers.Incus."
	initial := readSystemdReportMetrics(t, path)
	require.EqualValues(t, 1, initial[prefix+"databaseReadAvailable"])

	count := func() float64 {
		t.Helper()

		metrics := readSystemdReportMetrics(t, path)
		require.Contains(t, metrics, prefix+"unresolvedWarnings")

		return metrics[prefix+"unresolvedWarnings"]
	}

	baseline := count()

	createSystemdReportWarning(t, incusClient, "", "report global warning")
	require.Equal(t, baseline, count())

	createSystemdReportWarning(t, incusClient, daemon.State().ServerName, "report local warning")
	require.Equal(t, baseline+1, count())

	uuid := systemdReportWarningUUID(t, incusClient, "report local warning")
	require.NoError(t, incusClient.UpdateWarning(uuid, api.WarningPut{Status: "acknowledged"}, ""))
	require.Equal(t, baseline+1, count())

	require.NoError(t, incusClient.DeleteWarning(uuid))
	require.Equal(t, baseline, count())
}
