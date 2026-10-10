package systemd_report_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lxc/incus/v7/internal/server/systemd_report"
	"github.com/stretchr/testify/require"
)

func startServer(t *testing.T, snapshot systemd_report.Snapshot) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	server, err := systemd_report.Start(path, func(context.Context) (systemd_report.Snapshot, error) { return snapshot, nil })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Close()) })

	return path
}

func call(t *testing.T, path string, request string) []map[string]json.RawMessage {
	t.Helper()

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = conn.Write([]byte(request + "\x00"))
	require.NoError(t, err)

	reader := bufio.NewReader(conn)
	var replies []map[string]json.RawMessage

	for {
		data, err := reader.ReadBytes(0)
		require.NoError(t, err)

		var reply map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data[:len(data)-1], &reply))

		replies = append(replies, reply)

		if string(reply["continues"]) != "true" {
			return replies
		}
	}
}

func listMetrics(t *testing.T, path string) map[string]float64 {
	t.Helper()

	out := make(map[string]float64)

	for _, reply := range call(t, path, `{"method":"io.systemd.Metrics.List","more":true}`) {
		require.Empty(t, reply["error"])

		var metric struct {
			Name   string            `json:"name"`
			Fields map[string]string `json:"fields"`
			Value  float64           `json:"value"`
		}

		require.NoError(t, json.Unmarshal(reply["parameters"], &metric))

		if metric.Name == "org.linuxcontainers.Incus.unresolvedWarnings" {
			require.Empty(t, metric.Fields)
		}

		key := metric.Name
		if metric.Fields["type"] != "" {
			key += "/" + metric.Fields["type"]
		}

		_, exists := out[key]
		require.False(t, exists, "Duplicate metric %q", key)

		out[key] = metric.Value
	}

	return out
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
		{
			name:     "warnings unavailable",
			snapshot: systemd_report.Snapshot{DatabaseReadAvailable: true, UnresolvedWarnings: 3},
			want:     map[string]float64{prefix + "databaseReadAvailable": 1},
		},
		{
			name:     "no warnings",
			snapshot: systemd_report.Snapshot{DatabaseReadAvailable: true, WarningsAvailable: true},
			want:     map[string]float64{prefix + "databaseReadAvailable": 1, prefix + "unresolvedWarnings": 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := startServer(t, tc.snapshot)

			require.Equal(t, tc.want, listMetrics(t, path))
		})
	}
}

func TestSystemdReportDescribe(t *testing.T) {
	path := startServer(t, systemd_report.Snapshot{})

	descriptions := make(map[string]string)
	replies := call(t, path, `{"method":"io.systemd.Metrics.Describe","more":true}`)
	require.Len(t, replies, 3)

	for _, reply := range replies {
		require.Empty(t, reply["error"])

		var family struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Type        string `json:"type"`
		}

		require.NoError(t, json.Unmarshal(reply["parameters"], &family))
		require.NotEmpty(t, family.Description)

		descriptions[family.Name] = family.Type
	}

	prefix := "org.linuxcontainers.Incus."
	require.Equal(t, map[string]string{
		prefix + "databaseReadAvailable": "gauge",
		prefix + "instanceTypeAvailable": "gauge",
		prefix + "unresolvedWarnings":    "gauge",
	}, descriptions)
}

func TestSystemdReportUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	s, err := systemd_report.Start(path, func(context.Context) (systemd_report.Snapshot, error) {
		return systemd_report.Snapshot{}, errors.New("shutting down")
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	replies := call(t, path, `{"method":"io.systemd.Metrics.List","more":true}`)
	require.Len(t, replies, 1)
	require.Equal(t, `"io.systemd.Metrics.Unavailable"`, string(replies[0]["error"]))
}

func TestSystemdReportSocketLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	source := func(context.Context) (systemd_report.Snapshot, error) { return systemd_report.Snapshot{}, nil }
	s, err := systemd_report.Start(path, source)
	require.NoError(t, err)

	file, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o666), file.Mode().Perm())

	_, err = systemd_report.Start(path, source)
	require.ErrorContains(t, err, "already in use")

	require.NoError(t, s.Close())

	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSystemdReportRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "org.linuxcontainers.Incus")
	source := func(context.Context) (systemd_report.Snapshot, error) { return systemd_report.Snapshot{}, nil }

	require.NoError(t, os.Symlink(filepath.Join(dir, "target"), path))

	_, err := systemd_report.Start(path, source)
	require.ErrorContains(t, err, "not a socket")

	_, err = os.Lstat(path)
	require.NoError(t, err)

	unsafeDir := filepath.Join(dir, "unsafe")
	require.NoError(t, os.Mkdir(unsafeDir, 0o755))
	require.NoError(t, os.Chmod(unsafeDir, 0o777))

	_, err = systemd_report.Start(filepath.Join(unsafeDir, "org.linuxcontainers.Incus"), source)
	require.ErrorContains(t, err, "not secure")
}

func TestSystemdReportRejectsUnexpectedRequests(t *testing.T) {
	path := startServer(t, systemd_report.Snapshot{})

	for _, tc := range []struct {
		name    string
		request string
		want    string
	}{
		{"oneway", `{"method":"io.systemd.Metrics.List","more":true,"oneway":true}`, "InvalidParameter"},
		{"unexpected field", `{"method":"io.systemd.Metrics.List","more":true,"path":"/1.0/instances"}`, "InvalidParameter"},
		{"unexpected parameter", `{"method":"io.systemd.Metrics.List","more":true,"parameters":{"project":"default"}}`, "InvalidParameter"},
		{"unknown method", `{"method":"io.systemd.Metrics.Delete","more":true}`, "MethodNotFound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := call(t, path, tc.request)
			require.Len(t, replies, 1)
			require.JSONEq(t, `"org.varlink.service.`+tc.want+`"`, string(replies[0]["error"]))
		})
	}
}

func TestSystemdReportReplacesOnlyStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.linuxcontainers.Incus")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	listener.SetUnlinkOnClose(false)
	require.NoError(t, listener.Close())

	source := func(context.Context) (systemd_report.Snapshot, error) { return systemd_report.Snapshot{}, nil }
	s, err := systemd_report.Start(path, source)
	require.NoError(t, err)

	require.NoError(t, os.Remove(path))

	other, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, other.Close()) })

	require.NoError(t, s.Close())

	_, err = os.Lstat(path)
	require.NoError(t, err)
}
