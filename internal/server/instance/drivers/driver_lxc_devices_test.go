package drivers

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

func TestRemoveUnixDevicesAfterFailedRestore(t *testing.T) {
	if os.Getenv("INCUS_TEST_UNIX_MOUNT_NAMESPACE") != "1" {
		if os.Geteuid() != 0 {
			t.Skip("Requires root and a private mount namespace")
		}

		cmd := exec.Command(os.Args[0], "-test.run=^TestRemoveUnixDevicesAfterFailedRestore$", "-test.v")
		cmd.Env = append(os.Environ(), "INCUS_TEST_UNIX_MOUNT_NAMESPACE=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNS}
		output, err := cmd.CombinedOutput()
		if errors.Is(err, unix.EPERM) {
			t.Skip("Mount namespaces are unavailable")
		}

		require.NoError(t, err, string(output))
		t.Log(string(output))
		return
	}

	err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	require.NoError(t, err)
	t.Setenv("INCUS_DIR", t.TempDir())
	d := &lxc{common: common{
		name:    "restore-failure",
		project: api.Project{Name: "default"},
		logger:  logger.Log,
	}}
	require.NoError(t, os.MkdirAll(d.DevicesPath(), 0o700))
	source := filepath.Join(t.TempDir(), "source")
	require.NoError(t, os.WriteFile(source, []byte("device"), 0o600))

	for _, name := range []string{"unix.test", "forkmknod.unix.test", "infiniband.unix.test", "unrelated"} {
		path := filepath.Join(d.DevicesPath(), name)
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		if name != "unrelated" {
			require.NoError(t, unix.Mount(source, path, "", unix.MS_BIND, ""))
			require.ErrorIs(t, os.Remove(path), unix.EBUSY)
		}
	}

	// Restore can fail after device mounts are prepared, before stop hooks can remove them.
	d.cleanupFailedMigrationRestore()
	for _, name := range []string{"unix.test", "forkmknod.unix.test", "infiniband.unix.test"} {
		_, err := os.Stat(filepath.Join(d.DevicesPath(), name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}

	require.FileExists(t, filepath.Join(d.DevicesPath(), "unrelated"))
	require.FileExists(t, source)
	require.NoError(t, os.WriteFile(filepath.Join(d.DevicesPath(), "unix.unmounted"), nil, 0o600))
	require.NoError(t, d.removeUnixDevices())
	require.NoFileExists(t, filepath.Join(d.DevicesPath(), "unix.unmounted"))
	require.NoError(t, d.removeUnixDevices())

	// Unexpected unmount errors must reach the caller and leave the mountpoint intact.
	path := filepath.Join(d.DevicesPath(), "unix.denied")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.NoError(t, unix.Mount(source, path, "", unix.MS_BIND, ""))
	defer func() { _ = unix.Unmount(path, unix.MNT_DETACH) }()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var caps [2]unix.CapUserData
	require.NoError(t, unix.Capget(&header, &caps[0]))
	restricted := caps
	restricted[0].Effective &^= 1 << unix.CAP_SYS_ADMIN
	require.NoError(t, unix.Capset(&header, &restricted[0]))
	err = d.removeUnixDevices()
	require.NoError(t, unix.Capset(&header, &caps[0]))
	require.ErrorIs(t, err, unix.EPERM)
	require.FileExists(t, path)
	require.NoError(t, d.removeUnixDevices())
}
