package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/response"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/ws"
)

const execWSControl = -1
const execWSStdin = 0
const execWSStdout = 1
const execWSStderr = 2

var execCmd = APIEndpoint{
	Name: "exec",
	Path: "exec",

	Post: APIEndpointAction{Handler: execPost},
}

func execPost(d *Daemon, r *http.Request) response.Response {
	post := api.InstanceExecPost{}

	buf, err := io.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	err = json.Unmarshal(buf, &post)
	if err != nil {
		return response.BadRequest(err)
	}

	if !post.WaitForWS {
		return response.BadRequest(fmt.Errorf("Websockets are required for VM exec"))
	}

	env := map[string]string{}

	if post.Environment != nil {
		for k, v := range post.Environment {
			env[k] = v
		}
	}

	// Set default value for PATH
	_, ok := env["PATH"]
	if !ok {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	if util.PathExists("/snap/bin") {
		env["PATH"] = fmt.Sprintf("%s:/snap/bin", env["PATH"])
	}

	// If running as root, set some env variables
	if post.User == 0 {
		// Set default value for HOME
		_, ok = env["HOME"]
		if !ok {
			env["HOME"] = "/root"
		}

		// Set default value for USER
		_, ok = env["USER"]
		if !ok {
			env["USER"] = "root"
		}
	}

	// Set default value for LANG
	_, ok = env["LANG"]
	if !ok {
		env["LANG"] = "C.UTF-8"
	}

	// Set the default working directory
	if post.Cwd == "" {
		post.Cwd = env["HOME"]
		if post.Cwd == "" {
			post.Cwd = "/"
		}
	}

	ws := &execWs{}
	ws.fds = map[int]string{}

	ws.conns = map[int]*websocket.Conn{}
	ws.conns[execWSControl] = nil
	ws.conns[0] = nil // This is used for either TTY or Stdin.
	if !post.Interactive {
		ws.conns[execWSStdout] = nil
		ws.conns[execWSStderr] = nil
	}

	ws.requiredConnectedCtx, ws.requiredConnectedDone = context.WithCancel(context.Background())
	ws.interactive = post.Interactive

	for i := range ws.conns {
		ws.fds[i], err = internalUtil.RandomHexString(32)
		if err != nil {
			return response.InternalError(err)
		}
	}

	ws.command = post.Command
	ws.env = env

	ws.width = post.Width
	ws.height = post.Height

	ws.cwd = post.Cwd
	ws.uid = post.User
	ws.gid = post.Group

	resources := map[string][]api.URL{}

	op, err := operations.OperationCreate(nil, "", operations.OperationClassWebsocket, operationtype.CommandExec, resources, ws.Metadata(), ws.Do, nil, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	// Link the operation to the agent's event server.
	op.SetEventServer(d.events)

	return operations.OperationResponse(op)
}

type execWs struct {
	command               []string
	env                   map[string]string
	conns                 map[int]*websocket.Conn
	connsLock             sync.Mutex
	requiredConnectedCtx  context.Context
	requiredConnectedDone func()
	interactive           bool
	fds                   map[int]string
	width                 int
	height                int
	uid                   uint32
	gid                   uint32
	cwd                   string
}

func (s *execWs) Metadata() any {
	fds := jmap.Map{}
	for fd, secret := range s.fds {
		if fd == execWSControl {
			fds[api.SecretNameControl] = secret
		} else {
			fds[strconv.Itoa(fd)] = secret
		}
	}

	return jmap.Map{
		"fds":         fds,
		"command":     s.command,
		"environment": s.env,
		"interactive": s.interactive,
	}
}

func (s *execWs) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return fmt.Errorf("missing secret")
	}

	for fd, fdSecret := range s.fds {
		if secret == fdSecret {
			conn, err := ws.Upgrader.Upgrade(w, r, nil)
			if err != nil {
				return err
			}

			s.connsLock.Lock()
			defer s.connsLock.Unlock()

			val, found := s.conns[fd]
			if found && val == nil {
				s.conns[fd] = conn

				for _, c := range s.conns {
					if c == nil {
						return nil // Not all required connections connected yet.
					}
				}

				s.requiredConnectedDone() // All required connections now connected.
				return nil
			} else if !found {
				return fmt.Errorf("Unknown websocket number")
			} else {
				return fmt.Errorf("Websocket number already connected")
			}
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this Operation actually exists */
	return os.ErrPermission
}

func (s *execWs) Do(op *operations.Operation) error {
	// Once this function ends ensure that any connected websockets are closed.
	defer func() {
		s.connsLock.Lock()
		for i := range s.conns {
			if s.conns[i] != nil {
				_ = s.conns[i].Close()
			}
		}
		s.connsLock.Unlock()
	}()

	// As this function only gets called when the exec request has WaitForWS enabled, we expect the client to
	// connect to all of the required websockets within a short period of time and we won't proceed until then.
	logger.Debug("Waiting for exec websockets to connect")
	select {
	case <-s.requiredConnectedCtx.Done():
		break
	case <-time.After(time.Second * 5):
		return fmt.Errorf("Timed out waiting for websockets to connect")
	}

	var err error
	var ttys []*os.File
	var ptys []*os.File

	var stdin *os.File
	var stdout *os.File
	var stderr *os.File

	if s.interactive {
		ttys = make([]*os.File, 1)
		ptys = make([]*os.File, 1)
		ptys[0], ttys[0], err = linux.OpenPty(int64(s.uid), int64(s.gid))
		if err != nil {
			return err
		}

		stdin = ttys[0]
		stdout = ttys[0]
		stderr = ttys[0]

		if s.width > 0 && s.height > 0 {
			_ = linux.SetPtySize(int(ptys[0].Fd()), s.width, s.height)
		}
	} else {
		ttys = make([]*os.File, 3)
		ptys = make([]*os.File, 3)
		for i := 0; i < len(ttys); i++ {
			ptys[i], ttys[i], err = os.Pipe()
			if err != nil {
				return err
			}
		}

		stdin = ptys[execWSStdin]
		stdout = ttys[execWSStdout]
		stderr = ttys[execWSStderr]
	}

	waitAttachedChildIsDead, markAttachedChildIsDead := context.WithCancel(context.Background())
	var wgEOF sync.WaitGroup

	finisher := func(cmdResult int, cmdErr error) error {
		// Cancel this before closing the control connection so control handler can detect command ending.
		markAttachedChildIsDead()

		for _, tty := range ttys {
			_ = tty.Close()
		}

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()

		if conn != nil {
			_ = conn.Close() // Close control connection (will cause control go routine to end).
		}

		wgEOF.Wait()

		for _, pty := range ptys {
			_ = pty.Close()
		}

		metadata := jmap.Map{"return": cmdResult}
		err = op.UpdateMetadata(metadata)
		if err != nil {
			return err
		}

		return cmdErr
	}

	var cmd *exec.Cmd

	if len(s.command) > 1 {
		cmd = exec.Command(s.command[0], s.command[1:]...)
	} else {
		cmd = exec.Command(s.command[0])
	}

	// Prepare the environment
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: s.uid,
			Gid: s.gid,
		},
		// Creates a new session if the calling process is not a process group leader.
		// The calling process is the leader of the new session, the process group leader of
		// the new process group, and has no controlling terminal.
		// This is important to allow remote shells to handle ctrl+c.
		Setsid: true,
	}

	// Make the given terminal the controlling terminal of the calling process.
	// The calling process must be a session leader and not have a controlling terminal already.
	// This is important as allows ctrl+c to work as expected for non-shell programs.
	if s.interactive {
		cmd.SysProcAttr.Setctty = true
	}

	cmd.Dir = s.cwd

	err = cmd.Start()
	if err != nil {
		exitStatus := -1

		if errors.Is(err, exec.ErrNotFound) || os.IsNotExist(err) {
			exitStatus = 127
		} else if errors.Is(err, fs.ErrPermission) {
			exitStatus = 126
		}

		return finisher(exitStatus, err)
	}

	l := logger.AddContext(logger.Ctx{"PID": cmd.Process.Pid, "interactive": s.interactive})
	l.Debug("Instance process started")

	wgEOF.Add(1)
	go func() {
		defer wgEOF.Done()

		l.Debug("Exec control handler started")
		defer l.Debug("Exec control handler finished")

		s.connsLock.Lock()
		conn := s.conns[-1]
		s.connsLock.Unlock()

		for {
			mt, r, err := conn.NextReader()
			if err != nil || mt == websocket.CloseMessage {
				// Check if command process has finished normally, if so, no need to kill it.
				if waitAttachedChildIsDead.Err() != nil {
					return
				}

				if mt == websocket.CloseMessage {
					l.Warn("Got exec control websocket close message, killing command")
				} else {
					l.Warn("Failed getting exec control websocket reader, killing command", logger.Ctx{"err": err})
				}

				err := unix.Kill(cmd.Process.Pid, unix.SIGKILL)
				if err != nil {
					l.Error("Failed to send SIGKILL")
				} else {
					l.Info("Sent SIGKILL")
				}

				return
			}

			buf, err := io.ReadAll(r)
			if err != nil {
				// Check if command process has finished normally, if so, no need to kill it.
				if waitAttachedChildIsDead.Err() != nil {
					return
				}

				l.Warn("Failed reading control websocket message, killing command", logger.Ctx{"err": err})

				return
			}

			command := api.InstanceExecControl{}
			err = json.Unmarshal(buf, &command)
			if err != nil {
				l.Debug("Failed to unmarshal control socket command", logger.Ctx{"err": err})
				continue
			}

			if command.Command == "window-resize" && s.interactive {
				winchWidth, err := strconv.Atoi(command.Args["width"])
				if err != nil {
					l.Debug("Unable to extract window width", logger.Ctx{"err": err})
					continue
				}

				winchHeight, err := strconv.Atoi(command.Args["height"])
				if err != nil {
					l.Debug("Unable to extract window height", logger.Ctx{"err": err})
					continue
				}

				err = linux.SetPtySize(int(ptys[0].Fd()), winchWidth, winchHeight)
				if err != nil {
					l.Debug("Failed to set window size", logger.Ctx{"err": err, "width": winchWidth, "height": winchHeight})
					continue
				}
			} else if command.Command == "signal" {
				err := unix.Kill(cmd.Process.Pid, unix.Signal(command.Signal))
				if err != nil {
					l.Debug("Failed forwarding signal", logger.Ctx{"err": err, "signal": command.Signal})
					continue
				}

				l.Info("Forwarded signal", logger.Ctx{"signal": command.Signal})
			}
		}
	}()

	if s.interactive {
		wgEOF.Add(1)
		go func() {
			defer wgEOF.Done()

			l.Debug("Exec mirror websocket started", logger.Ctx{"number": 0})
			defer l.Debug("Exec mirror websocket finished", logger.Ctx{"number": 0})

			s.connsLock.Lock()
			conn := s.conns[0]
			s.connsLock.Unlock()

			readDone, writeDone := ws.Mirror(conn, linux.NewExecWrapper(waitAttachedChildIsDead, ptys[0]))

			<-readDone
			<-writeDone
			_ = conn.Close()
		}()
	} else {
		wgEOF.Add(len(ttys) - 1)
		for i := 0; i < len(ttys); i++ {
			go func(i int) {
				l.Debug("Exec mirror websocket started", logger.Ctx{"number": i})
				defer l.Debug("Exec mirror websocket finished", logger.Ctx{"number": i})

				if i == 0 {
					s.connsLock.Lock()
					conn := s.conns[i]
					s.connsLock.Unlock()

					<-ws.MirrorWrite(conn, ttys[i])
					_ = ttys[i].Close()
				} else {
					s.connsLock.Lock()
					conn := s.conns[i]
					s.connsLock.Unlock()

					<-ws.MirrorRead(conn, ptys[i])
					_ = ptys[i].Close()
					wgEOF.Done()
				}
			}(i)
		}
	}

	exitStatus, err := linux.ExitStatus(cmd.Wait())

	l.Debug("Instance process stopped", logger.Ctx{"err": err, "exitStatus": exitStatus})
	return finisher(exitStatus, nil)
}
