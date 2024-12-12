package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	liblxc "github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/ws"
)

type consoleWs struct {
	// instance currently worked on
	instance instance.Instance

	// websocket connections to bridge pty fds to
	conns map[int]*websocket.Conn

	// map dynamic websocket connections to their associated console file
	dynamic map[*websocket.Conn]*os.File

	// locks needed to access the "conns" member
	connsLock sync.Mutex

	// channel to wait until all websockets are properly connected
	allConnected chan bool

	// channel to wait until the control socket is connected
	controlConnected chan bool

	// map file descriptors to secret
	fds map[int]string

	// terminal width
	width int

	// terminal height
	height int

	// channel type (either console or vga)
	protocol string
}

func (s *consoleWs) Metadata() any {
	fds := jmap.Map{}
	for fd, secret := range s.fds {
		if fd == -1 {
			fds[api.SecretNameControl] = secret
		} else {
			fds[strconv.Itoa(fd)] = secret
		}
	}

	return jmap.Map{"fds": fds}
}

func (s *consoleWs) Connect(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	switch s.protocol {
	case instance.ConsoleTypeConsole:
		return s.connectConsole(op, r, w)
	case instance.ConsoleTypeVGA:
		return s.connectVGA(op, r, w)
	default:
		return fmt.Errorf("Unknown protocol %q", s.protocol)
	}
}

func (s *consoleWs) connectConsole(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
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
			s.conns[fd] = conn
			s.connsLock.Unlock()

			if fd == -1 {
				s.controlConnected <- true
				return nil
			}

			s.connsLock.Lock()
			for i, c := range s.conns {
				if i != -1 && c == nil {
					s.connsLock.Unlock()
					return nil
				}
			}
			s.connsLock.Unlock()

			s.allConnected <- true
			return nil
		}
	}

	/* If we didn't find the right secret, the user provided a bad one,
	 * which 403, not 404, since this operation actually exists */
	return os.ErrPermission
}

func (s *consoleWs) connectVGA(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
	secret := r.FormValue("secret")
	if secret == "" {
		return fmt.Errorf("missing secret")
	}

	for fd, fdSecret := range s.fds {
		if secret != fdSecret {
			continue
		}

		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return err
		}

		if fd == -1 {
			logger.Debug("VGA control websocket connected")

			s.connsLock.Lock()
			s.conns[fd] = conn
			s.connsLock.Unlock()

			s.controlConnected <- true
			return nil
		}

		logger.Debug("VGA dynamic websocket connected")

		console, _, err := s.instance.Console("vga")
		if err != nil {
			_ = conn.Close()
			return err
		}

		// Mirror the console and websocket.
		go func() {
			l := logger.AddContext(logger.Ctx{"address": conn.RemoteAddr().String()})

			defer l.Debug("Finished mirroring websocket to console")

			l.Debug("Started mirroring websocket")
			readDone, writeDone := ws.Mirror(conn, console)

			<-readDone
			l.Debug("Finished mirroring console to websocket")
			<-writeDone
		}()

		s.connsLock.Lock()
		s.dynamic[conn] = console
		s.connsLock.Unlock()

		return nil
	}

	// If we didn't find the right secret, the user provided a bad one,
	// which 403, not 404, since this operation actually exists.
	return os.ErrPermission
}

func (s *consoleWs) Do(op *operations.Operation) error {
	s.instance.SetOperation(op)

	switch s.protocol {
	case instance.ConsoleTypeConsole:
		return s.doConsole(op)
	case instance.ConsoleTypeVGA:
		return s.doVGA(op)
	default:
		return fmt.Errorf("Unknown protocol %q", s.protocol)
	}
}

func (s *consoleWs) doConsole(op *operations.Operation) error {
	defer logger.Debug("Console websocket finished")
	<-s.allConnected

	// Get console from instance.
	console, consoleDisconnectCh, err := s.instance.Console(s.protocol)
	if err != nil {
		return err
	}

	// Cleanup the console when we're done.
	defer func() {
		_ = console.Close()
	}()

	// Detect size of window and set it into console.
	if s.width > 0 && s.height > 0 {
		_ = linux.SetPtySize(int(console.Fd()), s.width, s.height)
	}

	consoleDoneCh := make(chan struct{})

	// Wait for control socket to connect and then read messages from the remote side in a loop.
	go func() {
		defer logger.Debugf("Console control websocket finished")
		res := <-s.controlConnected
		if !res {
			return
		}

		for {
			s.connsLock.Lock()
			conn := s.conns[-1]
			s.connsLock.Unlock()

			_, r, err := conn.NextReader()
			if err != nil {
				logger.Debugf("Got error getting next reader: %v", err)
				close(consoleDoneCh)
				return
			}

			buf, err := io.ReadAll(r)
			if err != nil {
				logger.Debugf("Failed to read message: %v", err)
				break
			}

			command := api.InstanceConsoleControl{}

			err = json.Unmarshal(buf, &command)
			if err != nil {
				logger.Debugf("Failed to unmarshal control socket command: %s", err)
				continue
			}

			if command.Command == "window-resize" {
				winchWidth, err := strconv.Atoi(command.Args["width"])
				if err != nil {
					logger.Debugf("Unable to extract window width: %s", err)
					continue
				}

				winchHeight, err := strconv.Atoi(command.Args["height"])
				if err != nil {
					logger.Debugf("Unable to extract window height: %s", err)
					continue
				}

				err = linux.SetPtySize(int(console.Fd()), winchWidth, winchHeight)
				if err != nil {
					logger.Debugf("Failed to set window size to: %dx%d", winchWidth, winchHeight)
					continue
				}

				logger.Debugf("Set window size to: %dx%d", winchWidth, winchHeight)
			}
		}
	}()

	// Mirror the console and websocket.
	mirrorDoneCh := make(chan struct{})
	go func() {
		s.connsLock.Lock()
		conn := s.conns[0]
		s.connsLock.Unlock()

		l := logger.AddContext(logger.Ctx{"address": conn.RemoteAddr().String()})
		defer l.Debug("Finished mirroring websocket to console")

		l.Debug("Started mirroring websocket")
		readDone, writeDone := ws.Mirror(conn, console)

		<-readDone
		l.Debug("Finished mirroring console to websocket")
		<-writeDone
		close(mirrorDoneCh)
	}()

	// Wait until either the console or the websocket is done.
	select {
	case <-mirrorDoneCh:
		close(consoleDisconnectCh)
	case <-consoleDoneCh:
		close(consoleDisconnectCh)
	}

	// Get the console and control websockets.
	s.connsLock.Lock()
	consoleConn := s.conns[0]
	ctrlConn := s.conns[-1]
	s.connsLock.Unlock()

	defer func() {
		_ = consoleConn.Close()
		_ = ctrlConn.Close()
	}()

	// Write a reset escape sequence to the console to cancel any ongoing reads to the handle
	// and then close it. This ordering is important, close the console before closing the
	// websocket to ensure console doesn't get stuck reading.
	_, _ = console.Write([]byte("\x1bc"))

	err = console.Close()
	if err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}

	// Indicate to the control socket go routine to end if not already.
	close(s.controlConnected)
	return nil
}

func (s *consoleWs) doVGA(op *operations.Operation) error {
	defer logger.Debug("VGA websocket finished")

	consoleDoneCh := make(chan struct{})

	// The control socket is used to terminate the operation.
	go func() {
		defer logger.Debugf("VGA control websocket finished")
		res := <-s.controlConnected
		if !res {
			return
		}

		for {
			s.connsLock.Lock()
			conn := s.conns[-1]
			s.connsLock.Unlock()

			_, _, err := conn.NextReader()
			if err != nil {
				logger.Debugf("Got error getting next reader: %v", err)
				close(consoleDoneCh)
				return
			}
		}
	}()

	// Wait until the control channel is done.
	<-consoleDoneCh
	s.connsLock.Lock()
	control := s.conns[-1]
	s.connsLock.Unlock()
	err := control.Close()

	// Close all dynamic connections.
	for conn, console := range s.dynamic {
		_ = conn.Close()
		_ = console.Close()
	}

	// Indicate to the control socket go routine to end if not already.
	close(s.controlConnected)

	return err
}

// Cancel is responsible for closing websocket connections.
func (s *consoleWs) Cancel(op *operations.Operation) error {
	s.connsLock.Lock()
	conn := s.conns[-1]
	s.connsLock.Unlock()

	if conn == nil {
		return nil
	}

	_ = conn.Close()

	// Close all dynamic connections.
	for conn, console := range s.dynamic {
		_ = conn.Close()
		_ = console.Close()
	}

	return nil
}

// swagger:operation POST /1.0/instances/{name}/console instances instance_console_post
//
//	Connect to console
//
//	Connects to the console of an instance.
//
//	The returned operation metadata will contain two websockets, one for data and one for control.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: console
//	    description: Console request
//	    schema:
//	      $ref: "#/definitions/InstanceConsolePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceConsolePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	post := api.InstanceConsolePost{}
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		return response.BadRequest(err)
	}

	err = json.Unmarshal(buf, &post)
	if err != nil {
		return response.BadRequest(err)
	}

	// Forward the request if the container is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(s, projectName, name, r, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		url := api.NewURL().Path(version.APIVersion, "instances", name, "console").Project(projectName)
		resp, _, err := client.RawQuery("POST", url.String(), post, "")
		if err != nil {
			return response.SmartError(err)
		}

		opAPI, err := resp.MetadataAsOperation()
		if err != nil {
			return response.SmartError(err)
		}

		return operations.ForwardedOperationResponse(projectName, opAPI)
	}

	if post.Type == "" {
		post.Type = instance.ConsoleTypeConsole
	}

	// Basic parameter validation.
	if !slices.Contains([]string{instance.ConsoleTypeConsole, instance.ConsoleTypeVGA}, post.Type) {
		return response.BadRequest(fmt.Errorf("Unknown console type %q", post.Type))
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if post.Type == instance.ConsoleTypeVGA && inst.Type() != instancetype.VM {
		return response.BadRequest(fmt.Errorf("VGA console is only supported by virtual machines"))
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Instance is not running"))
	}

	if inst.IsFrozen() {
		return response.BadRequest(fmt.Errorf("Instance is frozen"))
	}

	// Find any running 'ConsoleShow' operation for the instance.
	// If the '--force' flag was used, cancel the running operation. Otherwise, notify the user about the operation.
	for _, op := range operations.Clone() {
		// Consider only console show operations with Running status.
		if op.Type() != operationtype.ConsoleShow || op.Project() != projectName || op.Status() != api.Running {
			continue
		}

		// Fetch instance name from operation.
		r := op.Resources()
		apiUrls := r["instances"]
		if len(apiUrls) < 1 {
			return response.SmartError(fmt.Errorf("Operation does not have an instance URL defined"))
		}

		urlPrefix, instanceName := path.Split(apiUrls[0].URL.Path)
		if urlPrefix == "" || instanceName == "" {
			return response.SmartError(fmt.Errorf("Instance URL has incorrect format"))
		}

		if instanceName != inst.Name() {
			continue
		}

		if !post.Force {
			return response.SmartError(fmt.Errorf("This console is already connected. Force is required to take it over."))
		}

		_, err = op.Cancel()
		if err != nil {
			return response.SmartError(err)
		}
	}

	ws := &consoleWs{}
	ws.fds = map[int]string{}
	ws.conns = map[int]*websocket.Conn{}
	ws.conns[-1] = nil
	ws.conns[0] = nil
	ws.dynamic = map[*websocket.Conn]*os.File{}
	for i := -1; i < len(ws.conns)-1; i++ {
		ws.fds[i], err = internalUtil.RandomHexString(32)
		if err != nil {
			return response.InternalError(err)
		}
	}

	ws.allConnected = make(chan bool, 1)
	ws.controlConnected = make(chan bool, 1)
	ws.instance = inst
	ws.width = post.Width
	ws.height = post.Height
	ws.protocol = post.Type

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", ws.instance.Name())}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassWebsocket, operationtype.ConsoleShow, resources, ws.Metadata(), ws.Do, ws.Cancel, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/console instances instance_console_get
//
//	Get console output
//
//	Gets the console output for the instance either as text log or as vga
//	screendump.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: type
//	    description: Console type
//	    type: string
//	    enum: [log, vga]
//	    default: log
//	    example: vga
//	responses:
//	  "200":
//	     description: |
//	       Console output either as raw console log or as vga screendump in PNG
//	       format depending on the `type` parameter provided with the request.
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	       image/png:
//	         schema:
//	           type: string
//	           format: binary
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceConsoleLogGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	consoleLogType := request.QueryParam(r, "type")
	if consoleLogType != "" && consoleLogType != "log" && consoleLogType != "vga" {
		return response.SmartError(fmt.Errorf("Invalid value for type parameter: %s", consoleLogType))
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Forward the request if the container is remote.
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{}

	if !inst.IsRunning() {
		// Check if we have data we can return.
		consoleBufferLogPath := inst.ConsoleBufferLogPath()
		if !util.PathExists(consoleBufferLogPath) {
			return response.FileResponse(r, nil, nil)
		}

		ent.Path = consoleBufferLogPath
		ent.Filename = consoleBufferLogPath
		return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
	}

	if inst.Type() == instancetype.Container {
		c, ok := inst.(instance.Container)
		if !ok {
			return response.SmartError(fmt.Errorf("Failed to cast inst to Container"))
		}

		// Query the container's console ringbuffer.
		console := liblxc.ConsoleLogOptions{
			ClearLog:       false,
			ReadLog:        true,
			ReadMax:        0,
			WriteToLogFile: true,
		}

		// Send a ringbuffer request to the container.
		logContents, err := c.ConsoleLog(console)
		if err != nil {
			errno, isErrno := linux.GetErrno(err)
			if !isErrno {
				return response.SmartError(err)
			}

			if errno == unix.ENODATA {
				return response.FileResponse(r, nil, nil)
			}

			return response.SmartError(err)
		}

		ent.File = bytes.NewReader([]byte(logContents))
		ent.FileModified = time.Now()
		ent.FileSize = int64(len(logContents))

		return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
	} else if inst.Type() == instancetype.VM {
		v, ok := inst.(instance.VM)
		if !ok {
			return response.SmartError(fmt.Errorf("Failed to cast inst to VM"))
		}

		var headers map[string]string
		if consoleLogType == "vga" {
			screenshotFile, err := os.CreateTemp(v.Path(), "screenshot-*.png")
			if err != nil {
				return response.SmartError(fmt.Errorf("Couldn't create screenshot file: %w", err))
			}

			ent.Cleanup = func() {
				_ = screenshotFile.Close()
				_ = os.Remove(screenshotFile.Name())
			}

			err = v.ConsoleScreenshot(screenshotFile)
			if err != nil {
				return response.SmartError(err)
			}

			fileInfo, err := screenshotFile.Stat()
			if err != nil {
				return response.SmartError(fmt.Errorf("Couldn't stat screenshot file for filesize: %w", err))
			}

			headers = map[string]string{
				"Content-Type": "image/png",
			}

			ent.File = screenshotFile
			ent.FileSize = fileInfo.Size()
			ent.Filename = screenshotFile.Name()
		} else {
			logContents, err := v.ConsoleLog()
			if err != nil {
				return response.SmartError(err)
			}

			ent.File = bytes.NewReader([]byte(logContents))
			ent.FileSize = int64(len(logContents))
		}

		ent.FileModified = time.Now()

		return response.FileResponse(r, []response.FileResponseEntry{ent}, headers)
	}

	return response.SmartError(fmt.Errorf("Unsupported instance type %q", inst.Type()))
}

// swagger:operation DELETE /1.0/instances/{name}/console instances instance_console_delete
//
//	Clear the console log
//
//	Clears the console log buffer.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceConsoleLogDelete(d *Daemon, r *http.Request) response.Response {
	if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 3, 0, 0) {
		return response.BadRequest(fmt.Errorf("Clearing the console buffer requires liblxc >= 3.0"))
	}

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	projectName := request.ProjectParam(r)

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.Container {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	c, ok := inst.(instance.Container)
	if !ok {
		return response.SmartError(fmt.Errorf("Instance is not container type"))
	}

	truncateConsoleLogFile := func(path string) error {
		// Check that this is a regular file. We don't want to try and unlink
		// /dev/stderr or /dev/null or something.
		st, err := os.Stat(path)
		if err != nil {
			return err
		}

		if !st.Mode().IsRegular() {
			return fmt.Errorf("The console log is not a regular file")
		}

		if path == "" {
			return fmt.Errorf("Container does not keep a console logfile")
		}

		return os.Truncate(path, 0)
	}

	if !inst.IsRunning() {
		consoleLogpath := c.ConsoleBufferLogPath()
		return response.SmartError(truncateConsoleLogFile(consoleLogpath))
	}

	// Send a ringbuffer request to the container.
	console := liblxc.ConsoleLogOptions{
		ClearLog:       true,
		ReadLog:        false,
		ReadMax:        0,
		WriteToLogFile: false,
	}

	_, err = c.ConsoleLog(console)
	if err != nil {
		errno, isErrno := linux.GetErrno(err)
		if !isErrno {
			return response.SmartError(err)
		}

		if errno == unix.ENODATA {
			return response.SmartError(nil)
		}

		return response.SmartError(err)
	}

	return response.SmartError(nil)
}
