package systemd_report

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lxc/incus/v7/shared/logger"
	"github.com/varlink/go/varlink"
	"golang.org/x/sys/unix"
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
error Unavailable()
`

type metricsInterface struct {
	snapshot SnapshotFunc
}

func (m *metricsInterface) VarlinkGetName() string {
	return "io.systemd.Metrics"
}

func (m *metricsInterface) VarlinkGetDescription() string {
	return interfaceDescription
}

func (m *metricsInterface) VarlinkDispatch(ctx context.Context, call varlink.Call, method string) error {
	if method != "List" && method != "Describe" {
		return call.ReplyMethodNotFound(ctx, "io.systemd.Metrics."+method)
	}

	if !call.WantsMore() {
		return call.ReplyInvalidParameter(ctx, "more")
	}

	var request struct {
		Parameters json.RawMessage `json:"parameters"`
	}

	err := json.Unmarshal(*call.Request, &request)
	if err != nil {
		return call.ReplyInvalidParameter(ctx, "parameters")
	}

	var params map[string]json.RawMessage
	if len(request.Parameters) > 0 && string(request.Parameters) != "null" {
		err = json.Unmarshal(request.Parameters, &params)
		if err != nil {
			return call.ReplyInvalidParameter(ctx, "parameters")
		}
	}

	if len(params) > 0 {
		return call.ReplyInvalidParameter(ctx, "parameters")
	}

	if method == "Describe" {
		for i, family := range metricFamilies {
			call.Continues = i < len(metricFamilies)-1
			err := call.Reply(ctx, map[string]any{"name": family.name, "description": family.description, "type": "gauge"})
			if err != nil {
				return err
			}
		}

		return nil
	}

	snapshot, err := m.snapshot(ctx)
	if err != nil {
		logger.Error("Cannot publish Incus health metrics", logger.Ctx{"err": err})
		return call.ReplyError(ctx, "io.systemd.Metrics.Unavailable", nil)
	}

	samples := snapshot.samples()
	for i, sample := range samples {
		call.Continues = i < len(samples)-1
		err := call.Reply(ctx, sample)
		if err != nil {
			return err
		}
	}

	return nil
}

type connection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *connection) Read(_ context.Context, p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *connection) ReadBytes(_ context.Context, delim byte) ([]byte, error) {
	return c.reader.ReadBytes(delim)
}

func (c *connection) Write(_ context.Context, p []byte) (int, error) {
	return c.Conn.Write(p)
}

// Server serves only the systemd metrics Varlink interface on a local socket.
type Server struct {
	listener          *net.UnixListener
	file              os.FileInfo
	service           *varlink.Service
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

	service, err := varlink.NewService("Incus", "Incus metrics", "1", "https://linuxcontainers.org/incus/")
	if err != nil {
		return nil, err
	}

	err = service.RegisterInterface(&metricsInterface{snapshot: snapshot})
	if err != nil {
		return nil, err
	}

	err = ensureSystemdReportDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}

	listener, file, err := listenSystemdReportSocket(path)
	if err != nil {
		return nil, err
	}

	s := &Server{listener: listener, file: file, service: service, connections: make(map[net.Conn]struct{}), connectionsPerUID: make(map[uint32]int)}
	go s.serve()
	return s, nil
}

func ensureSystemdReportDirectory(dir string) error {
	_, statErr := os.Stat(dir)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}

	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return err
	}

	if errors.Is(statErr, os.ErrNotExist) {
		err = os.Chmod(dir, 0o755)
		if err != nil {
			return err
		}
	}

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
	c := &connection{Conn: conn, reader: r}
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

		if !validRequest(conn, request[:len(request)-1]) {
			cancel()
			return
		}

		err = s.service.HandleMessage(ctx, c, request[:len(request)-1])
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

func validRequest(conn net.Conn, request []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(request, &fields) != nil {
		return false
	}
	for key, value := range fields {
		switch key {
		case "method", "parameters", "more":
		case "oneway", "upgrade":
			var enabled bool
			if json.Unmarshal(value, &enabled) != nil || enabled {
				writeInvalidParameter(conn, key)
				return false
			}
		default:
			writeInvalidParameter(conn, "parameters")
			return false
		}
	}
	return true
}

func writeInvalidParameter(conn net.Conn, parameter string) {
	_, err := conn.Write([]byte(`{"error":"org.varlink.service.InvalidParameter","parameters":{"parameter":"` + parameter + `"}}` + "\x00"))
	if err != nil {
		logger.Debug("Failed sending Incus systemd-report error", logger.Ctx{"err": err})
	}
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

var _ varlink.ReadWriterContext = (*connection)(nil)
