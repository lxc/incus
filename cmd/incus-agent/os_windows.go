//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

var (
	osShutdownSignal       = os.Interrupt
	osBaseWorkingDirectory = "C:\\"
	osMetricsSupported     = false
	osGuestAPISupport      = false
)

func osGetEnvironment() (*api.ServerEnvironment, error) {
	serverName, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	env := &api.ServerEnvironment{
		Kernel:             "Windows",
		KernelArchitecture: runtime.GOARCH,
		Server:             "incus-agent",
		ServerPid:          os.Getpid(),
		ServerVersion:      version.Version,
		ServerName:         serverName,
	}

	return env, nil
}

func osLoadModules() error {
	// No OS drivers to load on Windows.
	return nil
}

func osMountShared(src string, dst string, fstype string, opts []string) error {
	return errors.New("Dynamic mounts aren't supported on Windows")
}

func osUmount(src string) error {
	return errors.New("Dynamic mounts aren't supported on Windows")
}

func osGetCPUMetrics(d *Daemon) ([]metrics.CPUMetrics, error) {
	return []metrics.CPUMetrics{}, errors.New("Metrics aren't supported on Windows")
}

func osGetDiskMetrics(d *Daemon) ([]metrics.DiskMetrics, error) {
	return []metrics.DiskMetrics{}, errors.New("Metrics aren't supported on Windows")
}

func osGetFilesystemMetrics(d *Daemon) ([]metrics.FilesystemMetrics, error) {
	return []metrics.FilesystemMetrics{}, errors.New("Metrics aren't supported on Windows")
}

func osGetMemoryMetrics(d *Daemon) (metrics.MemoryMetrics, error) {
	return metrics.MemoryMetrics{}, errors.New("Metrics aren't supported on Windows")
}

func osGetCPUState() api.InstanceStateCPU {
	return api.InstanceStateCPU{}
}

func osGetMemoryState() api.InstanceStateMemory {
	return api.InstanceStateMemory{}
}

func osGetNetworkState() map[string]api.InstanceStateNetwork {
	return map[string]api.InstanceStateNetwork{}
}

func osGetProcessesState() int64 {
	pids := make([]uint32, 65536)
	pidBytes := uint32(0)

	err := windows.EnumProcesses(pids, &pidBytes)
	if err != nil {
		return -1
	}

	return int64(pidBytes / 4)
}

func osGetOSState() *api.InstanceStateOSInfo {
	// Get Windows registry.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}

	defer k.Close()

	// Get local hostname.
	hostname, err := os.Hostname()
	if err != nil {
		return nil
	}

	// Get build info.
	v := *windows.RtlGetVersion()

	osVersion, _, err := k.GetStringValue("CurrentVersion")
	if err != nil {
		return nil
	}

	osName, _, err := k.GetStringValue("ProductName")
	if err != nil {
		return nil
	}

	osBuild, _, err := k.GetStringValue("CurrentBuild")
	if err != nil {
		return nil
	}

	// Windows 11 always self-reports as Windows 10.
	// The documented diferentiator is the build ID.
	if v.BuildNumber > 22000 {
		osName = strings.Replace(osName, "Windows 10", "Windows 11", 1)
	}

	// Prepare OS struct.
	osInfo := &api.InstanceStateOSInfo{
		OS:            osName,
		OSVersion:     osBuild,
		KernelVersion: osVersion,
		Hostname:      hostname,
		FQDN:          hostname,
	}

	return osInfo
}

func osReconfigureNetworkInterfaces() {
	// Agent assisted network reconfiguration isn't currently supported.
	return
}

func osGetInteractiveConsole(s *execWs) (io.ReadWriteCloser, io.ReadWriteCloser, error) {
	return nil, nil, errors.New("Only non-interactive exec sessions are currently supported on Windows")
}

func osPrepareExecCommand(s *execWs, cmd *exec.Cmd) {
	if s.cwd == "" {
		cmd.Dir = osBaseWorkingDirectory
	}

	return
}

func osHandleExecControl(control api.InstanceExecControl, s *execWs, pty io.ReadWriteCloser, cmd *exec.Cmd, l logger.Logger) {
	// Ignore control messages.
	return
}

func osExitStatus(err error) (int, error) {
	return 0, err
}

func osExecWrapper(ctx context.Context, pty io.ReadWriteCloser) io.ReadWriteCloser {
	return pty
}

func osGetListener(port int64) (net.Listener, error) {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on TCP: %w", err)
	}

	logger.Info("Started TCP listener")

	return l, nil
}

func osSetEnv(post *api.InstanceExecPost, env map[string]string) {
	env["PATH"] = "C:\\WINDOWS\\system32;C:\\WINDOWS"
}
