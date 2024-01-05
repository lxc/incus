package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/server/events"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/internal/server/ucred"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	apiGuest "github.com/lxc/incus/shared/api/guest"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/ws"
)

type hoistFunc func(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request)

// DevIncusServer creates an http.Server capable of handling requests against the
// /dev/incus Unix socket endpoint created inside containers.
func devIncusServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler:     devIncusAPI(d, hoistReq),
		ConnState:   pidMapper.ConnStateHandler,
		ConnContext: request.SaveConnectionInContext,
	}
}

type devIncusHandler struct {
	path string

	/*
	 * This API will have to be changed slightly when we decide to support
	 * websocket events upgrading, but since we don't have events on the
	 * server side right now either, I went the simple route to avoid
	 * needless noise.
	 */
	f func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response
}

var devIncusConfigGet = devIncusHandler{"/1.0/config", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if util.IsFalse(c.ExpandedConfig()["security.guestapi"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	filtered := []string{}
	for k := range c.ExpandedConfig() {
		if strings.HasPrefix(k, "user.") || strings.HasPrefix(k, "cloud-init.") {
			filtered = append(filtered, fmt.Sprintf("/1.0/config/%s", k))
		}
	}

	return response.DevIncusResponse(http.StatusOK, filtered, "json", c.Type() == instancetype.VM)
}}

var devIncusConfigKeyGet = devIncusHandler{"/1.0/config/{key}", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if util.IsFalse(c.ExpandedConfig()["security.guestapi"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	key, err := url.PathUnescape(mux.Vars(r)["key"])
	if err != nil {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusBadRequest, "bad request"), c.Type() == instancetype.VM)
	}

	if !strings.HasPrefix(key, "user.") && !strings.HasPrefix(key, "cloud-init.") {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	value, ok := c.ExpandedConfig()[key]
	if !ok {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusNotFound, "not found"), c.Type() == instancetype.VM)
	}

	return response.DevIncusResponse(http.StatusOK, value, "raw", c.Type() == instancetype.VM)
}}

var devIncusImageExport = devIncusHandler{"/1.0/images/{fingerprint}/export", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if util.IsFalse(c.ExpandedConfig()["security.guestapi"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	if util.IsFalseOrEmpty(c.ExpandedConfig()["security.guestapi.images"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	// Use by security checks to distinguish /dev/incus vs REST APs
	r.RemoteAddr = "@dev_incus"

	resp := imageExport(d, r)

	err := resp.Render(w)
	if err != nil {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
	}

	return response.DevIncusResponse(http.StatusOK, "", "raw", c.Type() == instancetype.VM)
}}

var devIncusMetadataGet = devIncusHandler{"/1.0/meta-data", func(d *Daemon, inst instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if util.IsFalse(inst.ExpandedConfig()["security.guestapi"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), inst.Type() == instancetype.VM)
	}

	value := inst.ExpandedConfig()["user.meta-data"]

	return response.DevIncusResponse(http.StatusOK, fmt.Sprintf("#cloud-config\ninstance-id: %s\nlocal-hostname: %s\n%s", inst.CloudInitID(), inst.Name(), value), "raw", inst.Type() == instancetype.VM)
}}

var devIncusEventsGet = devIncusHandler{"/1.0/events", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if util.IsFalse(c.ExpandedConfig()["security.guestapi"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "config,device"
	}

	var listenerConnection events.EventListenerConnection
	var resp response.Response

	// If the client has not requested a websocket connection then fallback to long polling event stream mode.
	if r.Header.Get("Upgrade") == "websocket" {
		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection = events.NewWebsocketListenerConnection(conn)

		resp = response.DevIncusResponse(http.StatusOK, "websocket", "websocket", c.Type() == instancetype.VM)
	} else {
		h, ok := w.(http.Hijacker)
		if !ok {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		conn, _, err := h.Hijack()
		if err != nil {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection, err = events.NewStreamListenerConnection(conn)
		if err != nil {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		resp = response.DevIncusResponse(http.StatusOK, "", "raw", c.Type() == instancetype.VM)
	}

	listener, err := d.State().DevIncusEvents.AddListener(c.ID(), listenerConnection, strings.Split(typeStr, ","))
	if err != nil {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
	}

	logger.Debug("New container event listener", logger.Ctx{"instance": c.Name(), "project": c.Project().Name, "listener_id": listener.ID})
	listener.Wait(r.Context())

	return resp
}}

var devIncusAPIHandler = devIncusHandler{"/1.0", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	s := d.State()

	if r.Method == "GET" {
		var location string
		if d.serverClustered {
			location = c.Location()
		} else {
			var err error

			location, err = os.Hostname()
			if err != nil {
				return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
			}
		}

		var state api.StatusCode

		if util.IsTrue(c.LocalConfig()["volatile.last_state.ready"]) {
			state = api.Ready
		} else {
			state = api.Started
		}

		return response.DevIncusResponse(http.StatusOK, apiGuest.DevIncusGet{APIVersion: version.APIVersion, Location: location, InstanceType: c.Type().String(), DevIncusPut: apiGuest.DevIncusPut{State: state.String()}}, "json", c.Type() == instancetype.VM)
	} else if r.Method == "PATCH" {
		if util.IsFalse(c.ExpandedConfig()["security.guestapi"]) {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
		}

		req := apiGuest.DevIncusPut{}

		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusBadRequest, err.Error()), c.Type() == instancetype.VM)
		}

		state := api.StatusCodeFromString(req.State)

		if state != api.Started && state != api.Ready {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid state %q", req.State), c.Type() == instancetype.VM)
		}

		err = c.VolatileSet(map[string]string{"volatile.last_state.ready": strconv.FormatBool(state == api.Ready)})
		if err != nil {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusInternalServerError, err.Error()), c.Type() == instancetype.VM)
		}

		if state == api.Ready {
			s.Events.SendLifecycle(c.Project().Name, lifecycle.InstanceReady.Event(c, nil))
		}

		return response.DevIncusResponse(http.StatusOK, "", "raw", c.Type() == instancetype.VM)
	}

	return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusMethodNotAllowed, fmt.Sprintf("method %q not allowed", r.Method)), c.Type() == instancetype.VM)

}}

var devIncusDevicesGet = devIncusHandler{"/1.0/devices", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if util.IsFalse(c.ExpandedConfig()["security.guestapi"]) {
		return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	// Populate NIC hwaddr from volatile if not explicitly specified.
	// This is so cloud-init running inside the instance can identify the NIC when the interface name is
	// different than the device name (such as when run inside a VM).
	localConfig := c.LocalConfig()
	devices := c.ExpandedDevices()
	for devName, devConfig := range devices {
		if devConfig["type"] == "nic" && devConfig["hwaddr"] == "" && localConfig[fmt.Sprintf("volatile.%s.hwaddr", devName)] != "" {
			devices[devName]["hwaddr"] = localConfig[fmt.Sprintf("volatile.%s.hwaddr", devName)]
		}
	}

	return response.DevIncusResponse(http.StatusOK, c.ExpandedDevices(), "json", c.Type() == instancetype.VM)
}}

var handlers = []devIncusHandler{
	{"/", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
		return response.DevIncusResponse(http.StatusOK, []string{"/1.0"}, "json", c.Type() == instancetype.VM)
	}},
	devIncusAPIHandler,
	devIncusConfigGet,
	devIncusConfigKeyGet,
	devIncusMetadataGet,
	devIncusEventsGet,
	devIncusImageExport,
	devIncusDevicesGet,
}

func hoistReq(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn := ucred.GetConnFromContext(r.Context())
		cred, ok := pidMapper.m[conn.(*net.UnixConn)]
		if !ok {
			http.Error(w, pidNotInContainerErr.Error(), http.StatusInternalServerError)
			return
		}

		s := d.State()

		c, err := findContainerForPid(cred.Pid, s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Access control
		rootUID := uint32(0)

		idmapset, err := c.CurrentIdmap()
		if err == nil && idmapset != nil {
			uid, _ := idmapset.ShiftIntoNS(0, 0)
			rootUID = uint32(uid)
		}

		if rootUID != cred.Uid {
			http.Error(w, "Access denied for non-root user", http.StatusUnauthorized)
			return
		}

		resp := f(d, c, w, r)
		_ = resp.Render(w)
	}
}

func devIncusAPI(d *Daemon, f hoistFunc) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range handlers {
		m.HandleFunc(handler.path, f(handler.f, d))
	}

	return m
}

/*
 * Everything below here is the guts of the unix socket bits. Unfortunately,
 * golang's API does not make this easy. What happens is:
 *
 * 1. We install a ConnState listener on the http.Server, which does the
 *    initial unix socket credential exchange. When we get a connection started
 *    event, we use SO_PEERCRED to extract the creds for the socket.
 *
 * 2. We store a map from the connection pointer to the pid for that
 *    connection, so that once the HTTP negotiation occurrs and we get a
 *    ResponseWriter, we know (because we negotiated on the first byte) which
 *    pid the connection belogs to.
 *
 * 3. Regular HTTP negotiation and dispatch occurs via net/http.
 *
 * 4. When rendering the response via ResponseWriter, we match its underlying
 *    connection against what we stored in step (2) to figure out which container
 *    it came from.
 */

/*
 * We keep this in a global so that we can reference it from the server and
 * from our http handlers, since there appears to be no way to pass information
 * around here.
 */
var pidMapper = ConnPidMapper{m: map[*net.UnixConn]*unix.Ucred{}}

type ConnPidMapper struct {
	m     map[*net.UnixConn]*unix.Ucred
	mLock sync.Mutex
}

func (m *ConnPidMapper) ConnStateHandler(conn net.Conn, state http.ConnState) {
	unixConn := conn.(*net.UnixConn)
	switch state {
	case http.StateNew:
		cred, err := linux.GetUcred(unixConn)
		if err != nil {
			logger.Debugf("Error getting ucred for conn %s", err)
		} else {
			m.mLock.Lock()
			m.m[unixConn] = cred
			m.mLock.Unlock()
		}

	case http.StateActive:
		return
	case http.StateIdle:
		return
	case http.StateHijacked:
		/*
		 * The "Hijacked" state indicates that the connection has been
		 * taken over from net/http. This is useful for things like
		 * developing websocket libraries, who want to upgrade the
		 * connection to a websocket one, and not use net/http any
		 * more. Whatever the case, we want to forget about it since we
		 * won't see it either.
		 */
		m.mLock.Lock()
		delete(m.m, unixConn)
		m.mLock.Unlock()
	case http.StateClosed:
		m.mLock.Lock()
		delete(m.m, unixConn)
		m.mLock.Unlock()
	default:
		logger.Debugf("Unknown state for connection %s", state)
	}
}

var pidNotInContainerErr = fmt.Errorf("pid not in container?")

func findContainerForPid(pid int32, s *state.State) (instance.Container, error) {
	/*
	 * Try and figure out which container a pid is in. There is probably a
	 * better way to do this. Based on rharper's initial performance
	 * metrics, looping over every container and loading them is
	 * expensive, so I wanted to avoid that if possible, so this happens in
	 * a two step process:
	 *
	 * 1. Walk up the process tree until you see something that looks like
	 *    an lxc monitor process and extract its name from there.
	 *
	 * 2. If this fails, it may be that someone did an `incus exec foo -- bash`,
	 *    so the process isn't actually a descendant of the container's
	 *    init. In this case we just look through all the containers until
	 *    we find an init with a matching pid namespace. This is probably
	 *    uncommon, so hopefully the slowness won't hurt us.
	 */

	origpid := pid

	for pid > 1 {
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(string(cmdline), "[lxc monitor]") {
			// container names can't have spaces
			parts := strings.Split(string(cmdline), " ")
			name := strings.TrimSuffix(parts[len(parts)-1], "\x00")

			projectName := api.ProjectDefaultName
			if strings.Contains(name, "_") {
				fields := strings.SplitN(name, "_", 2)
				projectName = fields[0]
				name = fields[1]
			}

			inst, err := instance.LoadByProjectAndName(s, projectName, name)
			if err != nil {
				return nil, err
			}

			if inst.Type() != instancetype.Container {
				return nil, fmt.Errorf("Instance is not container type")
			}

			return inst.(instance.Container), nil
		}

		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return nil, err
		}

		re, err := regexp.Compile(`^PPid:\s+([0-9]+)$`)
		if err != nil {
			return nil, err
		}

		for _, line := range strings.Split(string(status), "\n") {
			m := re.FindStringSubmatch(line)
			if len(m) > 1 {
				result, err := strconv.Atoi(m[1])
				if err != nil {
					return nil, err
				}

				pid = int32(result)
				break
			}
		}
	}

	origPidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", origpid))
	if err != nil {
		return nil, err
	}

	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		return nil, err
	}

	for _, inst := range instances {
		if inst.Type() != instancetype.Container {
			continue
		}

		if !inst.IsRunning() {
			continue
		}

		initpid := inst.InitPID()
		pidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", initpid))
		if err != nil {
			return nil, err
		}

		if origPidNs == pidNs {
			return inst.(instance.Container), nil
		}
	}

	return nil, pidNotInContainerErr
}
