//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/FuturFusion/vsock"
	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/lxc/incus/v7/internal/ports"
	"github.com/lxc/incus/v7/internal/server/metrics"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/revert"
)

var (
	// https://dev.to/cosmic_predator/writing-a-windows-service-in-go-1d1m
	osBaseWorkingDirectory = "C:\\"
	osAgentConfigPath      = "C:\\Program Files\\Incus-Agent\\incus-agent.yml"
	osVioSerialPath        = `\\.\org.linuxcontainers.incus`
	osShutdownSignal       = os.Interrupt
)

// Check for the VirtioSocketWSP service for vsock support.
func osLoadModules() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}

	defer m.Disconnect()

	viosockSvc := "VirtioSocketWSP"
	s, err := m.OpenService(viosockSvc)
	if err != nil {
		return err
	}

	defer s.Close()

	tryStart := func() (bool, error) {
		status, err := s.Query()
		if err != nil {
			return false, err
		}

		if status.State == svc.Stopped {
			err = s.Start()
			if err != nil {
				return false, err
			}
		}

		return status.State == svc.Running, nil
	}

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*5)
	defer cancel()

	// Try for 5s to start the service.
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Unable to start viosock service: %w", ctx.Err())
		default:
			running, err := tryStart()
			if err != nil {
				return err
			}

			if running {
				return nil
			}

			time.Sleep(time.Second)
		}
	}
}

func osGetListener(port int64) (net.Listener, error) {
	const CIDAny uint32 = 4294967295 // Equivalent to VMADDR_CID_ANY.

	// Setup the listener on wildcard CID for inbound connections from Incus.
	// We use the VMADDR_CID_ANY CID so that if the VM's CID changes in the future the listener still works.
	// A CID change can occur when restoring a stateful VM that was previously using one CID but is
	// subsequently restored using a different one.
	l, err := vsock.ListenContextID(CIDAny, ports.HTTPSDefaultPort, nil)
	if err != nil {
		return nil, fmt.Errorf("WINDOWS: Failed to listen on vsock: %w", err)
	}

	logger.Info("Started vsock listener")

	return l, nil
}

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

	d := newDaemon(m.agentCmd.global.flagLogDebug, m.agentCmd.global.flagLogVerbose, m.agentCmd.global.flagSecretsLocation)

	// Start the server.
	err := osLoadModules()
	if err == nil {
		err := startHTTPServer(d, m.agentCmd.global.flagLogDebug)
		if err != nil {
			changes <- svc.Status{State: svc.StopPending}
			elog.Error(1, fmt.Sprintf("Failed to start HTTP server: %s", err))
			return ssec, errno
		}
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	// Start status notifier in background.
	cancelStatusNotifier := m.agentCmd.startStatusNotifier(ctx, d.chConnected)
	defer cancelStatusNotifier()

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

	return ssec, errno
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

	defer logger.WarnOnError(elog.Close, "Failed to close event log")

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

	defer logger.WarnOnError(k.Close, "Failed to close registry key")

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

// cprRegex matches a terminal cursor position report.
var cprRegex = regexp.MustCompile(`\x1b\[[0-9]+;[0-9]+R`)

// conPty is a Windows pseudo console.
type conPty struct {
	console windows.Handle
	input   *os.File
	output  *os.File
	closed  chan struct{}

	mu       sync.Mutex
	cprTimer *time.Timer
}

// conPtyConsole releases the pseudo console, ending the conPty output stream.
type conPtyConsole struct {
	pty *conPty
}

// conPtyProcess is a process attached to a pseudo console.
type conPtyProcess struct {
	handle windows.Handle
	pid    int
	mu     sync.Mutex
	done   bool
}

func newConPty(width int, height int) (*conPty, error) {
	if width <= 0 || height <= 0 {
		width = 80
		height = 25
	}

	reverter := revert.New()
	defer reverter.Fail()

	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	// The child ends of the pipes are duplicated into conhost, we don't need them past creation.
	defer inputRead.Close()
	reverter.Add(func() { _ = inputWrite.Close() })

	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	defer outputWrite.Close()
	reverter.Add(func() { _ = outputRead.Close() })

	var console windows.Handle

	// Inherit the cursor position so the terminal isn't cleared on start.
	err = windows.CreatePseudoConsole(windows.Coord{X: int16(width), Y: int16(height)}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), windows.PSEUDOCONSOLE_INHERIT_CURSOR, &console)
	if err != nil {
		return nil, fmt.Errorf("Failed to create pseudo console: %w", err)
	}

	reverter.Success()

	return &conPty{console: console, input: inputWrite, output: outputRead, closed: make(chan struct{})}, nil
}

// Read reads the pseudo console output.
func (c *conPty) Read(p []byte) (int, error) {
	n, err := c.output.Read(p)

	// Conhost requests the cursor position on startup and blocks until it gets a report.
	// Answer it ourselves if the client isn't a terminal and doesn't reply in time.
	if bytes.Contains(p[:n], []byte("\x1b[6n")) {
		c.mu.Lock()
		c.cprTimer = time.AfterFunc(time.Second, func() {
			c.mu.Lock()
			defer c.mu.Unlock()

			if c.cprTimer == nil {
				return
			}

			c.cprTimer = nil
			_, _ = c.input.Write([]byte("\x1b[1;1R"))
		})
		c.mu.Unlock()
	}

	return n, err
}

// Write writes to the pseudo console input.
func (c *conPty) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.cprTimer != nil && cprRegex.Match(p) {
		c.cprTimer.Stop()
		c.cprTimer = nil
	}

	c.mu.Unlock()

	return c.input.Write(p)
}

// Close drains the pseudo console output, waits for its release and closes the pipes.
func (c *conPty) Close() error {
	c.mu.Lock()
	if c.cprTimer != nil {
		c.cprTimer.Stop()
		c.cprTimer = nil
	}

	c.mu.Unlock()

	// Conhost blocks on a full output pipe and can't exit until it's drained.
	_, _ = io.Copy(io.Discard, c.output)
	<-c.closed

	_ = c.input.Close()

	return c.output.Close()
}

// resize changes the pseudo console dimensions.
func (c *conPty) resize(width int, height int) error {
	return windows.ResizePseudoConsole(c.console, windows.Coord{X: int16(width), Y: int16(height)})
}

// start runs the command attached to the pseudo console.
func (c *conPty) start(ctx context.Context, cmd *exec.Cmd) (execProcess, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}

	defer attrs.Delete()

	// The attribute value is the pseudo console handle itself, not a pointer to it.
	err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&c.console)), unsafe.Sizeof(c.console))
	if err != nil {
		return nil, err
	}

	// Resolve extensions the same way exec.Cmd does on Windows.
	path, err := exec.LookPath(cmd.Path)
	if err != nil {
		return nil, err
	}

	appName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(cmd.Args))
	for _, arg := range cmd.Args {
		args = append(args, syscall.EscapeArg(arg))
	}

	cmdLine, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return nil, err
	}

	var dir *uint16
	if cmd.Dir != "" {
		dir, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}

	env, err := envBlock(cmd.Environ())
	if err != nil {
		return nil, err
	}

	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))

	var pi windows.ProcessInformation

	err = windows.CreateProcess(appName, cmdLine, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &env[0], dir, &si.StartupInfo, &pi)
	if err != nil {
		return nil, fmt.Errorf("Failed to start %q: %w", path, err)
	}

	_ = windows.CloseHandle(pi.Thread)

	proc := &conPtyProcess{handle: pi.Process, pid: int(pi.ProcessId)}
	context.AfterFunc(ctx, func() { _ = proc.Kill() })

	return proc, nil
}

// envBlock builds a sorted UTF-16 environment block from KEY=VALUE strings.
func envBlock(env []string) ([]uint16, error) {
	slices.SortFunc(env, func(a string, b string) int {
		keyA, _, _ := strings.Cut(a, "=")
		keyB, _, _ := strings.Cut(b, "=")

		return strings.Compare(strings.ToUpper(keyA), strings.ToUpper(keyB))
	})

	block := []uint16{}
	for _, kv := range env {
		encoded, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}

		block = append(block, encoded...)
	}

	return append(block, 0), nil
}

// Read always returns EOF as the console side isn't readable.
func (c *conPtyConsole) Read(p []byte) (int, error) {
	return 0, io.EOF
}

// Write always fails as the console side isn't writable.
func (c *conPtyConsole) Write(p []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// Close releases the pseudo console, flushing its output and closing the output pipe.
func (c *conPtyConsole) Close() error {
	// This blocks until the output has been drained, which happens in conPty.Close.
	go func() {
		windows.ClosePseudoConsole(c.pty.console)
		close(c.pty.closed)
	}()

	return nil
}

// Pid returns the process ID.
func (p *conPtyProcess) Pid() int {
	return p.pid
}

// Wait waits for the process to exit and returns its exit code.
func (p *conPtyProcess) Wait() (int, error) {
	_, err := windows.WaitForSingleObject(p.handle, windows.INFINITE)
	if err != nil {
		return -1, err
	}

	var code uint32

	err = windows.GetExitCodeProcess(p.handle, &code)

	p.mu.Lock()
	p.done = true
	_ = windows.CloseHandle(p.handle)
	p.mu.Unlock()

	if err != nil {
		return -1, err
	}

	return int(code), nil
}

// Kill terminates the process if it's still running.
func (p *conPtyProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.done {
		return os.ErrProcessDone
	}

	return windows.TerminateProcess(p.handle, 1)
}

func osGetInteractiveConsole(s *execWs) (io.ReadWriteCloser, io.ReadWriteCloser, error) {
	cp, err := newConPty(s.width, s.height)
	if err != nil {
		return nil, nil, err
	}

	return cp, &conPtyConsole{pty: cp}, nil
}

func osPrepareExecCommand(s *execWs, cmd *exec.Cmd) {
	if s.cwd == "" {
		cmd.Dir = osBaseWorkingDirectory
	}

	return
}

func osStartExecCommand(ctx context.Context, cmd *exec.Cmd, pty io.ReadWriteCloser) (execProcess, error) {
	cp, ok := pty.(*conPty)
	if ok {
		return cp.start(ctx, cmd)
	}

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return &cmdProcess{cmd: cmd}, nil
}

func osHandleExecControl(control api.InstanceExecControl, s *execWs, pty io.ReadWriteCloser, proc execProcess, l logger.Logger) {
	if control.Command == "signal" {
		sig := windows.Signal(control.Signal)

		// Interactive SIGINT is delivered as a Ctrl-C through the pseudo console.
		cp, ok := pty.(*conPty)
		if sig == windows.SIGINT && s.interactive && ok {
			_, err := cp.Write([]byte{0x03})
			if err != nil {
				l.Debug("Failed forwarding Ctrl-C", logger.Ctx{"err": err})
				return
			}

			l.Info("Forwarded Ctrl-C")
			return
		}

		// Windows has no signals, only handle those requesting termination.
		if !slices.Contains([]windows.Signal{windows.SIGHUP, windows.SIGINT, windows.SIGQUIT, windows.SIGABRT, windows.SIGKILL, windows.SIGTERM}, sig) {
			return
		}

		err := proc.Kill()
		if err != nil {
			l.Debug("Failed to terminate process", logger.Ctx{"err": err, "signal": control.Signal})
			return
		}

		l.Info("Terminated process", logger.Ctx{"signal": control.Signal})
		return
	}

	if control.Command != "window-resize" || !s.interactive {
		return
	}

	winchWidth, err := strconv.Atoi(control.Args["width"])
	if err != nil {
		l.Debug("Unable to extract window width", logger.Ctx{"err": err})
		return
	}

	winchHeight, err := strconv.Atoi(control.Args["height"])
	if err != nil {
		l.Debug("Unable to extract window height", logger.Ctx{"err": err})
		return
	}

	cp, ok := pty.(*conPty)
	if !ok {
		return
	}

	err = cp.resize(winchWidth, winchHeight)
	if err != nil {
		l.Debug("Failed to set window size", logger.Ctx{"err": err, "width": winchWidth, "height": winchHeight})
		return
	}
}

func osExitStatus(err error) (int, error) {
	if err == nil {
		return 0, nil
	}

	var exitErr *exec.ExitError

	// Detect and extract ExitError to check the embedded exit status.
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}

	return -1, err // Not able to extract an exit status.
}

func osSetEnv(post *api.InstanceExecPost, env map[string]string) {
	// SystemRoot is already set by default
	env["SystemDrive"] = "C:"

	// Program Files directories
	env["ProgramFiles"] = fmt.Sprintf("%s\\Program Files", env["SystemDrive"])
	env["ProgramFiles(x86)"] = fmt.Sprintf("%s (x86)", env["ProgramFiles"])
	env["ProgramW6432"] = fmt.Sprintf("%s", env["ProgramFiles"])
	env["CommonProgramFiles"] = fmt.Sprintf("%s\\Common Files", env["ProgramFiles"])
	env["CommonProgramFiles(x86)"] = fmt.Sprintf("%s (x86)\\Common Files", env["ProgramFiles"])
	env["CommonProgramW6432"] = fmt.Sprintf("%s\\Common Files", env["ProgramFiles"])

	// Windows directories
	env["WINDIR"] = fmt.Sprintf("%s\\WINDOWS", env["SystemDrive"])
	env["TMP"] = fmt.Sprintf("%s\\Temp", env["WINDIR"])
	env["TEMP"] = env["TMP"]

	// System32 directories
	system32 := fmt.Sprintf("%s\\System32", env["WINDIR"])
	env["ComSpec"] = fmt.Sprintf("%s\\cmd.exe", system32)
	env["DriverData"] = fmt.Sprintf("%s\\Drivers\\DriverData", system32)

	// User profile directories
	env["USERPROFILE"] = fmt.Sprintf("%s\\config\\systemprofile", system32)
	env["LOCALAPPDATA"] = fmt.Sprintf("%s\\AppData\\Local", env["USERPROFILE"])
	env["APPDATA"] = fmt.Sprintf("%s\\AppData\\Roaming", env["USERPROFILE"])

	// Miscellaneous
	env["COMPUTERNAME"] = ""
	env["PATH"] = fmt.Sprintf("%s;%s;%s\\WindowsPowerShell\\v1.0", system32, env["WINDIR"], system32)
	env["PATHEXT"] = ".COM;.EXE;.BAT;.CMD;.VBS;.VBE;.JS;.JSE;.WSF;.WSH;.MSC;.CPL"

	env["ProgramData"] = fmt.Sprintf("%s\\ProgramData", env["SystemDrive"])
	env["ALLUSERSPROFILE"] = env["ProgramData"]
	env["PUBLIC"] = fmt.Sprintf("%s\\Users\\Public", env["SystemDrive"])

	// Set the default working directory.
	if post.Cwd == "" {
		post.Cwd = system32
	}
}
