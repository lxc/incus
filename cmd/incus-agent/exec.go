package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/response"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/ws"
)

const (
	execWSControl = -1
	execWSStdin   = 0
	execWSStdout  = 1
	execWSStderr  = 2
)

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
		return response.BadRequest(errors.New("Websockets are required for VM exec"))
	}

	env := map[string]string{}

	if post.Environment != nil {
		maps.Copy(env, post.Environment)
	}

	osSetEnv(&post, env)

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
		return errors.New("missing secret")
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
				return errors.New("Unknown websocket number")
			} else {
				return errors.New("Websocket number already connected")
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
		return errors.New("Timed out waiting for websockets to connect")
	}

	var err error
	var ttys []io.ReadWriteCloser
	var ptys []io.ReadWriteCloser

	var stdin io.ReadCloser
	var stdout io.WriteCloser
	var stderr io.WriteCloser

	if s.interactive {
		ttys = make([]io.ReadWriteCloser, 1)
		ptys = make([]io.ReadWriteCloser, 1)

		ptys[0], ttys[0], err = osGetInteractiveConsole(s)
		if err != nil {
			return err
		}

		stdin = ttys[0]
		stdout = ttys[0]
		stderr = ttys[0]
	} else {
		ttys = make([]io.ReadWriteCloser, 3)
		ptys = make([]io.ReadWriteCloser, 3)
		for i := range ttys {
			ptys[i], ttys[i], err = os.Pipe()
			if err != nil {
				return err
			}
		}

		stdin = ptys[execWSStdin]
		stdout = ttys[execWSStdout]
		stderr = ttys[execWSStderr]
	}

	ctxCommand, cancel := context.WithCancel(context.Background())
	waitAttachedChildIsDead, markAttachedChildIsDead := context.WithCancel(context.Background())
	var wgEOF sync.WaitGroup

	finisher := func(cmdResult int, cmdErr error) error {
		// Cancel the context after we're done with cleanup.
		defer cancel()

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
		cmd = exec.CommandContext(ctxCommand, s.command[0], s.command[1:]...)
	} else {
		cmd = exec.CommandContext(ctxCommand, s.command[0])
	}

	// Prepare the environment
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = s.cwd

	osPrepareExecCommand(s, cmd)

	err = cmd.Start()
	if err != nil {
		exitStatus := -1

		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
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

				cancel()

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

			control := api.InstanceExecControl{}
			err = json.Unmarshal(buf, &control)
			if err != nil {
				l.Debug("Failed to unmarshal control socket command", logger.Ctx{"err": err})
				continue
			}

			osHandleExecControl(control, s, ptys[0], cmd, l)
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

			readDone, writeDone := ws.Mirror(conn, osExecWrapper(waitAttachedChildIsDead, ptys[0]))

			<-readDone
			<-writeDone
			_ = conn.Close()
		}()
	} else {
		wgEOF.Add(len(ttys) - 1)
		for i := range ttys {
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

	exitStatus, err := osExitStatus(cmd.Wait())

	l.Debug("Instance process stopped", logger.Ctx{"err": err, "exitStatus": exitStatus})
	return finisher(exitStatus, nil)
}
