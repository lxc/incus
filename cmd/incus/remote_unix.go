//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/ws"
)

type cmdRemoteProxy struct {
	global *cmdGlobal
	remote *cmdRemote

	flagTimeout int
}

// Parse the remote without connecting so the proxy can disable keepalive first.
var cmdRemoteProxyUsage = u.Usage{u.Colon(u.Remote), u.Target(u.Placeholder(i18n.G("unix socket file")))}

func (c *cmdRemoteProxy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("proxy", cmdRemoteProxyUsage...)
	cmd.Short = i18n.G("Run a local API proxy")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Run a local API proxy for the remote`,
	))

	cmd.RunE = c.run

	cli.AddIntFlag(cmd.Flags(), &c.flagTimeout, "timeout", i18n.G("Proxy timeout (exits when no connections)"), 0)

	return cmd
}

func (c *cmdRemoteProxy) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdRemoteProxyUsage, cmd, args)
	if err != nil {
		return err
	}

	remoteName := strings.TrimSuffix(parsed[0].String, ":")
	path := parsed[1].String

	remote, ok := c.global.conf.Remotes[remoteName]
	if !ok {
		return fmt.Errorf("The remote \"%s\" doesn't exist", remoteName)
	}

	remote.KeepAlive = 0

	// Attempt to read stdin for TLS connection details.
	_ = json.NewDecoder(os.Stdin).Decode(&remote.TLS)

	c.global.conf.Remotes[remoteName] = remote

	resources, err := c.global.parseServers(parsed[0].String)
	if err != nil {
		return err
	}

	s := resources[0].server

	// Create proxy socket.
	err = os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed to delete pre-existing unix socket: %w", err)
	}

	unixAddr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return fmt.Errorf("Unable to resolve unix socket: %w", err)
	}

	server, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return fmt.Errorf("Unable to setup unix socket: %w", err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		return fmt.Errorf("Unable to set unix socket permissions: %w", err)
	}

	// Get the connection info.
	info, err := s.GetConnectionInfo()
	if err != nil {
		return err
	}

	uri, err := url.Parse(info.URL)
	if err != nil {
		return err
	}

	// Enable keep-alive for proxied connections.
	httpClient, err := s.GetHTTPClient()
	if err != nil {
		return err
	}

	httpTransport, ok := httpClient.Transport.(*http.Transport)
	if ok {
		httpTransport.DisableKeepAlives = false
	}

	// Share upstream events connections between local clients.
	events := &remoteProxyEvents{
		s:    s,
		hubs: map[string]*remoteProxyEventsHub{},
	}

	// Get server info.
	api10, api10Etag, err := s.GetServer()
	if err != nil {
		return err
	}

	// Handle inbound connections.
	transport := remoteProxyTransport{
		s:       s,
		baseURL: uri,
	}

	connections := uint64(0)
	transactions := uint64(0)

	handler := remoteProxyHandler{
		s:         s,
		transport: transport,
		api10:     api10,
		api10Etag: api10Etag,
		events:    events,

		mu:           &sync.RWMutex{},
		connections:  &connections,
		transactions: &transactions,
	}

	// Handle the timeout.
	if c.flagTimeout > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(c.flagTimeout) * time.Second)

				// Check for active connections.
				handler.mu.RLock()
				if *handler.connections > 0 {
					handler.mu.RUnlock()
					continue
				}

				// Look for recent activity
				oldCount := uint64(*handler.transactions)
				handler.mu.RUnlock()

				time.Sleep(5 * time.Second)

				handler.mu.RLock()
				if oldCount == *handler.transactions {
					handler.mu.RUnlock()

					// Daemon has been inactive for 10s, exit.
					os.Exit(0) // nolint:revive
				}

				handler.mu.RUnlock()
			}
		}()
	}

	// Start the server.
	err = http.Serve(server, &handler)
	if err != nil {
		return err
	}

	return nil
}

type remoteProxyTransport struct {
	s incus.InstanceServer

	baseURL *url.URL
}

// RoundTrip handles an HTTP request.
func (t remoteProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Fix the request.
	r.URL.Scheme = t.baseURL.Scheme
	r.URL.Host = t.baseURL.Host
	r.RequestURI = ""

	resp, err := t.s.DoHTTP(r)
	if errors.Is(err, incus.ErrOIDCExpired) {
		// Override the response so the client knows to retry the request.
		resp.StatusCode = http.StatusUseProxy
		resp.Status = "Retry the request for OIDC refresh"

		return resp, nil
	}

	return resp, err
}

type remoteProxyHandler struct {
	s         incus.InstanceServer
	transport http.RoundTripper
	url       string

	mu           *sync.RWMutex
	connections  *uint64
	transactions *uint64

	api10     *api.Server
	api10Etag string
	events    *remoteProxyEvents

	token string
}

func (h *remoteProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Increase counters.
	defer func() {
		h.mu.Lock()
		*h.connections--
		h.mu.Unlock()
	}()

	h.mu.Lock()
	*h.transactions++
	*h.connections++
	h.mu.Unlock()

	// Don't allow cross-origin requests.
	origin := r.Header.Get("Origin")
	if origin != "" && origin != h.url {
		return
	}

	// Basic auth.
	if h.token != "" {
		// Parse query URL.
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return
		}

		token := values.Get("auth_token")
		if token != "" {
			// If a token was passed through the URL, persist it as a cookie.

			tokenCookie := http.Cookie{
				Name:     "auth_token",
				Value:    token,
				Path:     "/",
				Secure:   false,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			}

			http.SetCookie(w, &tokenCookie)
		} else {
			// If not, attempt to pull it from the cookie.
			cookie, err := r.Cookie("auth_token")
			if err != nil {
				// Fail authentication if no cookie can be found.
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			token = cookie.Value
		}

		// Check the user token against the expected value.
		if token != h.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	// Handle GET /1.0 internally (saves a round-trip).
	if r.Method == http.MethodGet && r.URL.Path == "/1.0" {
		// Parse query URL.
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return
		}

		// Only handle the request internally if project is the sole parameter.
		supported := true
		for k := range values {
			if k != "project" {
				supported = false
				break
			}
		}

		if supported {
			// Update project name to match.
			projectName := values.Get("project")
			if projectName == "" {
				projectName = api.ProjectDefaultName
			}

			// Get the cached data, refreshing it if needed.
			h.mu.RLock()
			cached := h.api10
			cachedEtag := h.api10Etag
			h.mu.RUnlock()

			if cached == nil {
				cached, cachedEtag, err = h.s.GetServer()
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				h.mu.Lock()
				h.api10 = cached
				h.api10Etag = cachedEtag
				h.mu.Unlock()
			}

			api10 := api.Server(*cached)
			api10.Environment.Project = projectName

			// Set the request headers.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", cachedEtag)
			w.WriteHeader(http.StatusOK)

			// Generate a body from the cached data.
			serverBody, err := json.Marshal(api10)
			if err != nil {
				return
			}

			apiResponse := api.Response{
				Type:       "sync",
				Status:     "success",
				StatusCode: 200,
				Metadata:   serverBody,
			}

			body, err := json.Marshal(apiResponse)
			if err != nil {
				return
			}

			_, _ = w.Write(body)

			return
		}
	}

	// Serve event streams from a shared upstream connection.
	if h.events != nil && r.Method == http.MethodGet && r.URL.Path == "/1.0/events" && websocket.IsWebSocketUpgrade(r) {
		if h.events.serve(w, r) {
			return
		}
	}

	// Forward everything else.
	proxy := httputil.ReverseProxy{
		Transport: h.transport,
		Rewrite:   func(*httputil.ProxyRequest) {},
	}

	proxy.ServeHTTP(w, r)

	// Invalidate the cache on modification of /1.0.
	if r.Method != http.MethodGet && r.URL.Path == "/1.0" {
		h.mu.Lock()
		h.api10 = nil
		h.api10Etag = ""
		h.mu.Unlock()
	}
}

// remoteProxyEvents shares upstream events websockets between local clients.
type remoteProxyEvents struct {
	s incus.InstanceServer

	mu   sync.Mutex
	hubs map[string]*remoteProxyEventsHub
}

// remoteProxyEventsHub is one upstream events connection and its local clients.
type remoteProxyEventsHub struct {
	upstream *websocket.Conn
	writeMu  sync.Mutex

	clients   map[*remoteProxyEventsClient]struct{}
	idleTimer *time.Timer
}

// remoteProxyEventsClient is a local events client, holding messages until its upgrade completes.
type remoteProxyEventsClient struct {
	conn    *websocket.Conn
	pending []remoteProxyEventsMessage
}

// remoteProxyEventsMessage is a websocket message waiting for a client upgrade to complete.
type remoteProxyEventsMessage struct {
	mt   int
	data []byte
}

// serve attaches a local events client to the shared upstream connection for its query.
func (e *remoteProxyEvents) serve(w http.ResponseWriter, r *http.Request) bool {
	query := r.URL.RawQuery

	hub, client, err := e.acquire(query)
	if err != nil {
		// Let the regular forwarding path relay the upstream error.
		return false
	}

	conn, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		e.release(query, hub, client)
		return true
	}

	// Deliver what arrived during the upgrade, then switch the client to live delivery.
	e.mu.Lock()
	for _, msg := range client.pending {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = conn.WriteMessage(msg.mt, msg.data)
	}

	client.pending = nil
	client.conn = conn
	e.mu.Unlock()

	// Forward anything the client sends and notice when it goes away.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

		hub.writeMu.Lock()
		_ = hub.upstream.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err = hub.upstream.WriteMessage(mt, data)
		hub.writeMu.Unlock()
		if err != nil {
			break
		}
	}

	e.release(query, hub, client)
	_ = conn.Close()

	return true
}

// acquire registers a new client with the hub for the query, connecting upstream when needed.
func (e *remoteProxyEvents) acquire(query string) (*remoteProxyEventsHub, *remoteProxyEventsClient, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	hub := e.hubs[query]
	if hub == nil {
		path := "/events"
		if query != "" {
			path += "?" + query
		}

		conn, err := e.s.RawWebsocket(path)
		if err != nil {
			return nil, nil, err
		}

		hub = &remoteProxyEventsHub{upstream: conn, clients: map[*remoteProxyEventsClient]struct{}{}}
		e.hubs[query] = hub

		go e.run(query, hub)
	}

	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
		hub.idleTimer = nil
	}

	client := &remoteProxyEventsClient{}
	hub.clients[client] = struct{}{}

	return hub, client, nil
}

// release drops a client and closes the upstream connection after a minute without clients.
func (e *remoteProxyEvents) release(query string, hub *remoteProxyEventsHub, client *remoteProxyEventsClient) {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(hub.clients, client)
	if len(hub.clients) > 0 || e.hubs[query] != hub {
		return
	}

	hub.idleTimer = time.AfterFunc(time.Minute, func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		if len(hub.clients) > 0 || e.hubs[query] != hub {
			return
		}

		delete(e.hubs, query)
		_ = hub.upstream.Close()
	})
}

// run relays upstream events and pings to the local clients until the upstream connection drops.
func (e *remoteProxyEvents) run(query string, hub *remoteProxyEventsHub) {
	broadcast := func(mt int, data []byte) {
		var conns []*websocket.Conn

		e.mu.Lock()
		for client := range hub.clients {
			if client.conn != nil {
				conns = append(conns, client.conn)
			} else if mt != websocket.PingMessage {
				client.pending = append(client.pending, remoteProxyEventsMessage{mt: mt, data: data})
			}
		}

		e.mu.Unlock()

		for _, conn := range conns {
			var err error

			deadline := time.Now().Add(5 * time.Second)
			if mt == websocket.PingMessage {
				err = conn.WriteControl(mt, data, deadline)
			} else {
				_ = conn.SetWriteDeadline(deadline)
				err = conn.WriteMessage(mt, data)
			}

			if err != nil {
				_ = conn.Close()
			}
		}
	}

	// The server pings every 10s, so a silent connection is a dead one.
	resetReadDeadline := func() {
		_ = hub.upstream.SetReadDeadline(time.Now().Add(30 * time.Second))
	}

	resetReadDeadline()

	defaultPingHandler := hub.upstream.PingHandler()
	hub.upstream.SetPingHandler(func(appData string) error {
		resetReadDeadline()
		broadcast(websocket.PingMessage, []byte(appData))

		return defaultPingHandler(appData)
	})

	for {
		mt, data, err := hub.upstream.ReadMessage()
		if err != nil {
			break
		}

		resetReadDeadline()
		broadcast(mt, data)
	}

	// Drop the hub and its clients, they reconnect through a fresh upstream connection.
	e.mu.Lock()
	if e.hubs[query] == hub {
		delete(e.hubs, query)
	}

	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
	}

	var conns []*websocket.Conn
	for client := range hub.clients {
		if client.conn != nil {
			conns = append(conns, client.conn)
		}
	}

	e.mu.Unlock()

	_ = hub.upstream.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
}
