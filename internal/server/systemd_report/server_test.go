package systemd_report_test

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

	"github.com/lxc/incus/v7/internal/server/systemd_report"
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
		snapshot systemd_report.Snapshot
		want     map[string]float64
	}{
		{
			name: "healthy",
			snapshot: systemd_report.Snapshot{
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
			snapshot: systemd_report.Snapshot{},
			want:     map[string]float64{prefix + "databaseReadAvailable": 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
			server, err := systemd_report.Start(path, func(context.Context) (systemd_report.Snapshot, error) { return tc.snapshot, nil })
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Close()) })

			require.Equal(t, tc.want, listMetrics(t, path))
		})
	}
}

func TestSystemdReportSocketLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	source := func(context.Context) (systemd_report.Snapshot, error) { return systemd_report.Snapshot{}, nil }

	// A stale socket left behind by a previous daemon gets replaced.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())

	s, err := systemd_report.Start(path, source)
	require.NoError(t, err)

	// A socket that is still in use is left alone.
	_, err = systemd_report.Start(path, source)
	require.ErrorContains(t, err, "already in use")

	require.NoError(t, s.Close())
	_, err = os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)

	// Close doesn't remove a socket that was replaced by someone else.
	s, err = systemd_report.Start(path, source)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))

	other, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() })

	require.NoError(t, s.Close())
	_, err = os.Lstat(path)
	require.NoError(t, err)
}
