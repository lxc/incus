//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"

	"github.com/lxc/incus/v6/internal/server/metrics"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// https://dev.to/cosmic_predator/writing-a-windows-service-in-go-1d1m
var osBaseWorkingDirectory = "C:\\"

// Start of Windows service code block
// Inspired of https://github.com/golang/sys/blob/master/windows/svc/example/service.go
var elog debug.Log

type incusAgentService struct {
	agentCmd *cmdAgent
}

func (m *incusAgentService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown

	tick := time.Tick(2 * time.Second)

	changes <- svc.Status{State: svc.StartPending}

	d := newDaemon(m.agentCmd.global.flagLogDebug, m.agentCmd.global.flagLogVerbose)

	// Start the server.
	err := startHTTPServer(d, m.agentCmd.global.flagLogDebug)
	if err != nil {
		changes <- svc.Status{State: svc.StopPending}
		elog.Error(1, fmt.Sprintf("Failed to start HTTP server: %s", err))
		return
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for {
		select {
		case <-tick:
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				break loop
			default:
				elog.Error(1, fmt.Sprintf("Unexpected control request #%d", c))
			}
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	return
}

func runService(name string, agentCmd *cmdAgent) error {
	var err error

	if agentCmd.global.flagLogDebug {
		elog = debug.New(name)
	} else {
		elog, err = eventlog.Open(name)
		if err != nil {
			return err
		}
	}

	defer elog.Close()

	elog.Info(1, fmt.Sprintf("Starting %s service", name))
	run := svc.Run
	if agentCmd.global.flagLogDebug {
		run = debug.Run
	}

	err = run(name, &incusAgentService{agentCmd: agentCmd})
	if err != nil {
		elog.Error(1, fmt.Sprintf("%s service failed: %v", name, err))
		return err
	}

	elog.Info(1, fmt.Sprintf("%s service stopped", name))
	return nil
}

// End of Windows service code block

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

func osMountShared(src string, dst string, fstype string, opts []string) error {
	return errors.New("Dynamic mounts aren't supported on Windows")
}

func osUmount(src string, dst string, fstype string) error {
	return errors.New("Dynamic mounts aren't supported on Windows")
}

func osGetFilesystemMetrics(d *Daemon) ([]metrics.FilesystemMetrics, error) {
	partitions, err := disk.Partitions(true)
	if err != nil {
		return nil, err
	}

	sort.Slice(partitions, func(i, j int) bool {
		return partitions[i].Mountpoint < partitions[j].Mountpoint
	})

	fsMetrics := make([]metrics.FilesystemMetrics, 0, len(partitions))
	for _, partition := range partitions {
		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			continue
		}

		fsMetrics = append(fsMetrics, metrics.FilesystemMetrics{
			Device:         partition.Device,
			Mountpoint:     partition.Mountpoint,
			FSType:         partition.Fstype,
			AvailableBytes: usage.Free,
			FreeBytes:      usage.Free,
			SizeBytes:      usage.Total,
		})
	}

	return fsMetrics, nil
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

func osSetEnv(post *api.InstanceExecPost, env map[string]string) {
	env["PATH"] = "C:\\WINDOWS\\system32;C:\\WINDOWS"
}
