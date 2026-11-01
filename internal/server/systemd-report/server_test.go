package systemdreport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	systemdreport "github.com/lxc/incus/v7/internal/server/systemd-report"
	"github.com/lxc/incus/v7/internal/version"
)

func listMetrics(t *testing.T, path string) map[string]float64 {
	t.Helper()

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = conn.Write([]byte(`{"method":"io.systemd.Metrics.List","more":true}` + "\x00"))
	require.NoError(t, err)

	reader := bufio.NewReader(conn)
	metrics := make(map[string]float64)

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
		if reply.Parameters.Fields["type"] != "" {
			key += "/" + reply.Parameters.Fields["type"]
		}

		metrics[key] = reply.Parameters.Value

		if !reply.Continues {
			return metrics
		}
	}
}

func TestSystemdReportMetrics(t *testing.T) {
	prefix := "org.linuxcontainers.Incus."

	for _, tc := range []struct {
		name     string
		snapshot systemdreport.Snapshot
		want     map[string]float64
	}{
		{
			name: "healthy",
			snapshot: systemdreport.Snapshot{
				DatabaseReadAvailable: true,
				WarningsAvailable:     true,
				UnresolvedWarnings:    3,
				InstanceTypes:         map[string]bool{"container": true, "virtual-machine": false},
			},
			want: map[string]float64{
				prefix + "databaseReadAvailable":                 1,
				prefix + "instanceTypeAvailable/container":       1,
				prefix + "instanceTypeAvailable/virtual-machine": 0,
				prefix + "unresolvedWarnings":                    3,
			},
		},
		{
			name:     "database unavailable",
			snapshot: systemdreport.Snapshot{},
			want:     map[string]float64{prefix + "databaseReadAvailable": 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0o700))
			path := filepath.Join(dir, "org.linuxcontainers.Incus")
			server, err := systemdreport.Start(path, func(context.Context) (systemdreport.Snapshot, error) { return tc.snapshot, nil })
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Close()) })

			require.Equal(t, tc.want, listMetrics(t, path))
		})
	}
}

func TestSystemdReportSocketLifecycle(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	path := filepath.Join(dir, "org.linuxcontainers.Incus")
	source := func(context.Context) (systemdreport.Snapshot, error) { return systemdreport.Snapshot{}, nil }

	// A stale socket left behind by a previous daemon gets replaced.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())

	s, err := systemdreport.Start(path, source)
	require.NoError(t, err)

	// A socket that is still in use is left alone.
	_, err = systemdreport.Start(path, source)
	require.ErrorContains(t, err, "already in use")

	require.NoError(t, s.Close())
	_, err = os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)

	// Close doesn't remove a socket that was replaced by someone else.
	s, err = systemdreport.Start(path, source)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))

	other, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })

	require.NoError(t, s.Close())
	_, err = os.Lstat(path)
	require.NoError(t, err)
}

func TestSystemdReportProtocol(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	path := filepath.Join(dir, "org.linuxcontainers.Incus")
	server, err := systemdreport.Start(path, func(context.Context) (systemdreport.Snapshot, error) {
		return systemdreport.Snapshot{}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Close()) })

	for _, tc := range []struct {
		name       string
		request    string
		replies    int
		error      string
		parameters map[string]any
	}{
		{
			name: "descriptions", request: `{"method":"io.systemd.Metrics.Describe","more":true}`,
			replies: 3, parameters: map[string]any{"type": "gauge"},
		},
		{
			name: "service version", request: `{"method":"org.varlink.service.GetInfo"}`,
			replies: 1, parameters: map[string]any{"version": version.Version},
		},
		{
			name: "missing more", request: `{"method":"io.systemd.Metrics.List"}`,
			replies: 1, error: "org.varlink.service.ExpectedMore",
		},
		{
			name: "unexpected parameter", request: `{"method":"io.systemd.Metrics.List","more":true,"parameters":{"unexpected":true}}`,
			replies: 1, error: "org.varlink.service.InvalidParameter", parameters: map[string]any{"parameter": "parameters"},
		},
		{
			name: "unknown method", request: `{"method":"io.systemd.Metrics.Unknown","more":true}`,
			replies: 1, error: "org.varlink.service.MethodNotFound", parameters: map[string]any{"method": "io.systemd.Metrics.Unknown"},
		},
		{
			name: "unknown interface", request: `{"method":"io.systemd.Unknown.List","more":true}`,
			replies: 1, error: "org.varlink.service.InterfaceNotFound", parameters: map[string]any{"interface": "io.systemd.Unknown"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("unix", path)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			reader := bufio.NewReader(conn)

			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
			_, err = conn.Write([]byte(tc.request + "\x00"))
			require.NoError(t, err)

			for i := range tc.replies {
				data, err := reader.ReadBytes(0)
				require.NoError(t, err)
				var reply struct {
					Parameters map[string]any `json:"parameters"`
					Continues  bool           `json:"continues"`
					Error      string         `json:"error"`
				}

				require.NoError(t, json.Unmarshal(data[:len(data)-1], &reply))
				require.Equal(t, tc.error, reply.Error)
				require.Equal(t, i < tc.replies-1, reply.Continues)
				for key, value := range tc.parameters {
					require.Equal(t, value, reply.Parameters[key])
				}
			}
		})
	}

	t.Run("interface description", func(t *testing.T) {
		conn, err := net.Dial("unix", path)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

		_, err = conn.Write([]byte(`{"method":"org.varlink.service.GetInterfaceDescription","parameters":{"interface":"io.systemd.Metrics"}}` + "\x00"))
		require.NoError(t, err)
		data, err := bufio.NewReader(conn).ReadBytes(0)
		require.NoError(t, err)

		var reply struct {
			Parameters struct {
				Description string `json:"description"`
			} `json:"parameters"`
			Continues bool   `json:"continues"`
			Error     string `json:"error"`
		}

		require.NoError(t, json.Unmarshal(data[:len(data)-1], &reply))
		require.Empty(t, reply.Error)
		require.False(t, reply.Continues)
		require.Contains(t, reply.Parameters.Description, "error NoSuchMetric()")
		require.NotContains(t, reply.Parameters.Description, "Unavailable")
	})
}
