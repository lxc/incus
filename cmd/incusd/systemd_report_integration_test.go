package main

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
	"github.com/lxc/incus/v7/internal/server/systemd_report"
	"github.com/lxc/incus/v7/shared/api"
)

func readUnresolvedWarnings(t *testing.T, path string) float64 {
	t.Helper()

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = conn.Write([]byte(`{"method":"io.systemd.Metrics.List","more":true}` + "\x00"))
	require.NoError(t, err)

	reader := bufio.NewReader(conn)
	for {
		data, err := reader.ReadBytes(0)
		require.NoError(t, err)

		var reply struct {
			Parameters struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"parameters"`
			Continues bool `json:"continues"`
		}

		require.NoError(t, json.Unmarshal(data[:len(data)-1], &reply))

		if reply.Parameters.Name == "org.linuxcontainers.Incus.unresolvedWarnings" {
			return reply.Parameters.Value
		}

		require.True(t, reply.Continues, "unresolvedWarnings metric not reported")
	}
}

func TestIntegration_SystemdReportNodeWarnings(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	t.Cleanup(cleanup)

	client, err := incus.ConnectIncusUnix(daemon.os.GetUnixSocket(), nil)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	source, err := systemd_report.Start(path, daemon.systemdReportSnapshot)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })

	createWarning := func(location string, message string) {
		_, _, err := client.RawQuery("POST", "/internal/debug/warnings", map[string]any{
			"location": location, "type_code": warningtype.Undefined, "entity_type_code": -1, "entity_id": -1, "message": message,
		}, "")
		require.NoError(t, err)
	}

	baseline := readUnresolvedWarnings(t, path)

	// Global warnings aren't counted.
	createWarning("", "global warning")
	require.Equal(t, baseline, readUnresolvedWarnings(t, path))

	createWarning(daemon.State().ServerName, "local warning")
	require.Equal(t, baseline+1, readUnresolvedWarnings(t, path))

	warnings, err := client.GetWarnings()
	require.NoError(t, err)

	var uuid string
	for _, warning := range warnings {
		if warning.LastMessage == "local warning" {
			uuid = warning.UUID
		}
	}

	require.NotEmpty(t, uuid)

	// Acknowledged warnings are still unresolved.
	require.NoError(t, client.UpdateWarning(uuid, api.WarningPut{Status: "acknowledged"}, ""))
	require.Equal(t, baseline+1, readUnresolvedWarnings(t, path))

	require.NoError(t, client.DeleteWarning(uuid))
	require.Equal(t, baseline, readUnresolvedWarnings(t, path))
}
