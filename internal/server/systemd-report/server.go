package systemdreport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/logger"
)

const (
	maxConnections           = 16
	maxConnectionsPerUID     = 4
	maxRequestsPerConnection = 32
	maxConnectionLifetime    = 2 * time.Minute
	idleTimeout              = 5 * time.Second
	callTimeout              = 25 * time.Second
)

const interfaceDescription = `interface io.systemd.Metrics

type MetricFamilyType (counter, gauge, string, object)

method List() -> (name: string, object: ?string, fields: ?[string]string, value: float)
method Describe() -> (name: string, description: string, type: MetricFamilyType)

error NoSuchMetric()
`

const serviceDescription = `interface org.varlink.service

method GetInfo() -> (vendor: string, product: string, version: string, url: string, interfaces: []string)
method GetInterfaceDescription(interface: string) -> (description: string)

error InterfaceNotFound(interface: string)
error MethodNotFound(method: string)
error MethodNotImplemented(method: string)
error InvalidParameter(parameter: string)
error PermissionDenied()
error ExpectedMore()
`

type request struct {
	Method     string         `json:"method"`
	Parameters map[string]any `json:"parameters"`
	More       bool           `json:"more"`
	Oneway     bool           `json:"oneway"`
	Upgrade    bool           `json:"upgrade"`
}

type reply struct {
	Parameters any    `json:"parameters,omitempty"`
	Continues  bool   `json:"continues,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Server serves only the systemd metrics Varlink interface on a local socket.
type Server struct {
	listener          *net.UnixListener
	file              os.FileInfo
	snapshot          SnapshotFunc
	mu                sync.Mutex
	connections       map[net.Conn]struct{}
	connectionsPerUID map[uint32]int
	closing           bool
}

// Start binds the public systemd-report socket without replacing an active service.
func Start(path string, snapshot SnapshotFunc) (*Server, error) {
	if snapshot == nil {
		return nil, errors.New("Missing Incus systemd-report snapshot source")
	}

	err := checkSystemdReportDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}

	listener, file, err := listenSystemdReportSocket(path)
	if err != nil {
		return nil, err
	}

	s := &Server{
		listener:          listener,
		file:              file,
		snapshot:          snapshot,
		connections:       make(map[net.Conn]struct{}),
		connectionsPerUID: make(map[uint32]int),
	}

	go s.serve()
	return s, nil
}

func checkSystemdReportDirectory(dir string) error {
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return err
	}

	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("Systemd-report socket directory %q is not secure", dir)
	}

	dirOwner, ok := dirInfo.Sys().(*syscall.Stat_t)
	if !ok || dirOwner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("Systemd-report socket directory %q is not owned by Incus", dir)
	}

	return nil
}

func listenSystemdReportSocket(path string) (*net.UnixListener, os.FileInfo, error) {
	file, err := os.Lstat(path)
	if err == nil {
		if file.Mode()&os.ModeSocket == 0 {
			return nil, nil, fmt.Errorf("Systemd-report socket path %q is not a socket", path)
		}

		info, ok := file.Sys().(*syscall.Stat_t)
		if !ok || info.Uid != uint32(os.Geteuid()) {
			return nil, nil, fmt.Errorf("Systemd-report socket %q is not owned by Incus", path)
		}

		conn, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			closeErr := conn.Close()
			return nil, nil, errors.Join(fmt.Errorf("Systemd-report socket %q is already in use", path), closeErr)
		}

		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, nil, fmt.Errorf("Cannot determine whether systemd-report socket %q is stale: %w", path, err)
		}

		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(file, current) {
			return nil, nil, fmt.Errorf("Systemd-report socket %q changed while checking it", path)
		}

		err = os.Remove(path)
		if err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}

	listener.SetUnlinkOnClose(false)

	file, err = os.Lstat(path)
	if err != nil {
		return nil, nil, errors.Join(err, listener.Close())
	}

	err = os.Chmod(path, 0o666)
	if err != nil {
		closeErr := listener.Close()
		current, statErr := os.Lstat(path)
		if statErr == nil && os.SameFile(file, current) {
			statErr = os.Remove(path)
		} else if errors.Is(statErr, os.ErrNotExist) {
			statErr = nil
		}

		return nil, nil, errors.Join(err, closeErr, statErr)
	}

	return listener, file, nil
}

// Client connections are best-effort cleanup; failures cannot be recovered here.
func closeClient(conn net.Conn) {
	err := conn.Close()
	if err != nil {
		logger.Debug("Failed closing Incus systemd-report connection", logger.Ctx{"err": err})
	}
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()

			if !closing {
				logger.Error("Cannot accept Incus systemd-report connection", logger.Ctx{"err": err})
			}

			return
		}

		uid, err := peerUID(conn)
		if err != nil {
			closeClient(conn)
			continue
		}

		s.mu.Lock()
		if s.closing || len(s.connections) >= maxConnections || s.connectionsPerUID[uid] >= maxConnectionsPerUID {
			s.mu.Unlock()
			closeClient(conn)
			continue
		}

		s.connections[conn] = struct{}{}
		s.connectionsPerUID[uid]++
		s.mu.Unlock()

		go func() {
			defer func() {
				s.mu.Lock()
				delete(s.connections, conn)
				s.connectionsPerUID[uid]--

				if s.connectionsPerUID[uid] == 0 {
					delete(s.connectionsPerUID, uid)
				}

				s.mu.Unlock()
				closeClient(conn)
			}()
			s.handle(conn)
		}()
	}
}

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}

	var cred *unix.Ucred
	var sockErr error

	err = raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return 0, err
	}

	if sockErr != nil {
		return 0, sockErr
	}

	if cred == nil {
		return 0, errors.New("Missing systemd-report socket peer credentials")
	}

	return cred.Uid, nil
}

func (s *Server) handle(conn *net.UnixConn) {
	r := bufio.NewReaderSize(conn, 64<<10)
	end := time.Now().Add(maxConnectionLifetime)
	for range maxRequestsPerConnection {
		err := conn.SetDeadline(minTime(time.Now().Add(idleTimeout), end))
		if err != nil {
			return
		}

		request, err := r.ReadSlice(0)
		if err != nil {
			return
		}

		err = conn.SetDeadline(minTime(time.Now().Add(callTimeout), end))
		if err != nil {
			return
		}

		ctx, cancel := context.WithDeadline(context.Background(), minTime(time.Now().Add(callTimeout), end))

		err = s.dispatch(ctx, conn, request[:len(request)-1])
		cancel()
		if err != nil {
			return
		}
	}
}

func minTime(a time.Time, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}

	return b
}

func (s *Server) dispatch(ctx context.Context, conn net.Conn, data []byte) error {
	var req request
	err := json.Unmarshal(data, &req)
	if err != nil {
		return err
	}

	if req.Oneway {
		return errors.New("Systemd-report does not support oneway calls")
	}

	if req.Upgrade {
		return writeError(conn, "InvalidParameter", map[string]string{"parameter": "upgrade"})
	}

	switch req.Method {
	case "io.systemd.Metrics.List", "io.systemd.Metrics.Describe", "org.varlink.service.GetInfo":
		if len(req.Parameters) != 0 {
			return writeError(conn, "InvalidParameter", map[string]string{"parameter": "parameters"})
		}
	}

	switch req.Method {
	case "io.systemd.Metrics.List", "io.systemd.Metrics.Describe":
		if !req.More {
			return writeError(conn, "ExpectedMore", nil)
		}

		if req.Method == "io.systemd.Metrics.Describe" {
			for i, family := range metricFamilies {
				err = writeReply(conn, reply{Parameters: map[string]string{"name": family.name, "description": family.description, "type": "gauge"}, Continues: i < len(metricFamilies)-1})
				if err != nil {
					return err
				}
			}

			return nil
		}

		snapshot, err := s.snapshot(ctx)
		if err != nil {
			logger.Error("Cannot publish Incus health metrics", logger.Ctx{"err": err})
			return err
		}

		samples := snapshot.samples()
		for i, sample := range samples {
			err = writeReply(conn, reply{Parameters: sample, Continues: i < len(samples)-1})
			if err != nil {
				return err
			}
		}

		return nil
	case "org.varlink.service.GetInfo":
		return writeReply(conn, reply{Parameters: map[string]any{
			"vendor":     "Incus",
			"product":    "Incus metrics",
			"version":    version.Version,
			"url":        "https://linuxcontainers.org/incus/",
			"interfaces": []string{"org.varlink.service", "io.systemd.Metrics"},
		}})
	case "org.varlink.service.GetInterfaceDescription":
		name, ok := req.Parameters["interface"].(string)
		if !ok || len(req.Parameters) != 1 || name == "" {
			return writeError(conn, "InvalidParameter", map[string]string{"parameter": "interface"})
		}

		switch name {
		case "io.systemd.Metrics":
			return writeReply(conn, reply{Parameters: map[string]string{"description": interfaceDescription}})
		case "org.varlink.service":
			return writeReply(conn, reply{Parameters: map[string]string{"description": serviceDescription}})
		default:
			return writeError(conn, "InterfaceNotFound", map[string]string{"interface": name})
		}

	default:
		index := strings.LastIndex(req.Method, ".")
		if index < 0 {
			return writeError(conn, "InvalidParameter", map[string]string{"parameter": "method"})
		}

		name := req.Method[:index]
		if name != "io.systemd.Metrics" && name != "org.varlink.service" {
			return writeError(conn, "InterfaceNotFound", map[string]string{"interface": name})
		}

		return writeError(conn, "MethodNotFound", map[string]string{"method": req.Method})
	}
}

func writeReply(conn net.Conn, response reply) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}

	_, err = conn.Write(append(data, 0))
	return err
}

func writeError(conn net.Conn, name string, parameters any) error {
	return writeReply(conn, reply{Error: "org.varlink.service." + name, Parameters: parameters})
}

// Close stops accepting requests and removes this server's own socket.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closing = true

	for conn := range s.connections {
		closeClient(conn)
	}

	s.mu.Unlock()

	// Stop accepting connections before cleaning up our own socket path.
	err := s.listener.Close()

	path := s.listener.Addr().String()
	file, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return err
	}

	if statErr != nil {
		return errors.Join(err, statErr)
	}

	if os.SameFile(file, s.file) {
		err = errors.Join(err, os.Remove(path))
	}

	return err
}
