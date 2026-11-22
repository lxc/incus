package main

// Cross-platform parts of the remote API proxy, shared by the remote proxy and webui commands.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/ws"
)

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
	uiDir string
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

	// Serve the UI from a local directory when configured.
	if h.uiDir != "" {
		if r.URL.Path == "/ui" {
			http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/ui/") {
			http.StripPrefix("/ui/", http.FileServer(cliUIHTTPDir{http.Dir(h.uiDir)})).ServeHTTP(w, r)
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

// cliUIHTTPDir is like incusd's uiHTTPDir: serve index.html for missing paths.
type cliUIHTTPDir struct {
	http.FileSystem
}

// Open is part of the http.FileSystem interface.
func (httpFS cliUIHTTPDir) Open(name string) (http.File, error) {
	fsFile, err := httpFS.FileSystem.Open(name)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return httpFS.FileSystem.Open("index.html")
	}

	return fsFile, err
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

// remoteProxyDialer keeps spare TLS connections ready to skip the handshake on the next request.
type remoteProxyDialer struct {
	dial func(ctx context.Context, network string, addr string) (net.Conn, error)
	min  int
	max  int

	mu      sync.Mutex
	conns   map[string][]*remoteProxyWarmConn
	filling map[string]int
	lastUse time.Time
	dials   []time.Time
	peaks   map[int64]int
}

// remoteProxyWarmConn is a spare connection with a watcher noticing when the server closes it.
type remoteProxyWarmConn struct {
	net.Conn

	network string
	addr    string

	taken bool
	dead  bool
	done  chan struct{}
}

func newRemoteProxyDialer(dial func(ctx context.Context, network string, addr string) (net.Conn, error), minSize int, maxSize int) *remoteProxyDialer {
	return &remoteProxyDialer{
		dial:    dial,
		min:     minSize,
		max:     maxSize,
		conns:   map[string][]*remoteProxyWarmConn{},
		filling: map[string]int{},
		peaks:   map[int64]int{},
	}
}

// size returns the pool size, following the largest burst of dials over the last minute.
func (d *remoteProxyDialer) size() int {
	size := d.min

	cutoff := time.Now().Unix() - 60
	for second, peak := range d.peaks {
		if second < cutoff {
			delete(d.peaks, second)
			continue
		}

		size = max(size, peak)
	}

	return min(size, d.max)
}

// record counts a dial towards the burst size, dials within a second of each other form a burst.
func (d *remoteProxyDialer) record() {
	now := time.Now()
	d.lastUse = now

	d.dials = slices.DeleteFunc(d.dials, func(t time.Time) bool { return now.Sub(t) > time.Second })
	d.dials = append(d.dials, now)

	second := now.Unix()
	d.peaks[second] = max(d.peaks[second], len(d.dials))
}

// Dial hands out a spare connection when available and tops up the pool.
func (d *remoteProxyDialer) Dial(ctx context.Context, network string, addr string) (net.Conn, error) {
	key := network + " " + addr

	d.mu.Lock()
	d.record()

	var warm *remoteProxyWarmConn

	pool := d.conns[key]
	if len(pool) > 0 {
		warm = pool[len(pool)-1]
		warm.taken = true
		d.conns[key] = pool[:len(pool)-1]
	}

	d.mu.Unlock()

	go d.fill(network, addr)

	if warm == nil {
		return d.dial(ctx, network, addr)
	}

	// Stop the watcher before handing the connection over, a timed out read leaves TLS usable.
	_ = warm.SetReadDeadline(time.Now())
	<-warm.done
	_ = warm.SetReadDeadline(time.Time{})

	if warm.dead {
		_ = warm.Close()
		return d.dial(ctx, network, addr)
	}

	return warm.Conn, nil
}

// fill dials spare connections in the background up to the pool size.
func (d *remoteProxyDialer) fill(network string, addr string) {
	key := network + " " + addr

	d.mu.Lock()
	missing := d.size() - len(d.conns[key]) - d.filling[key]
	if missing > 0 {
		d.filling[key] += missing
	}

	d.mu.Unlock()

	for range missing {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			conn, err := d.dial(ctx, network, addr)

			d.mu.Lock()
			defer d.mu.Unlock()

			d.filling[key]--
			if err != nil {
				return
			}

			// Check on it before the server's 3s request header timeout closes it.
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

			warm := &remoteProxyWarmConn{Conn: conn, network: network, addr: addr, done: make(chan struct{})}
			d.conns[key] = append(d.conns[key], warm)

			go d.watch(key, warm)
		}()
	}
}

// keepAlive sends a trivial request so the server keeps the spare connection for its 30s idle timeout.
func (d *remoteProxyDialer) keepAlive(warm *remoteProxyWarmConn) bool {
	_ = warm.SetDeadline(time.Now().Add(5 * time.Second))

	req, err := http.NewRequest(http.MethodHead, "https://"+warm.addr+"/", nil)
	if err != nil {
		return false
	}

	err = req.Write(warm)
	if err != nil {
		return false
	}

	// Anything left in the buffer would be lost to the next user of the connection.
	br := bufio.NewReader(warm)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return false
	}

	_, err = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.Close || br.Buffered() > 0 {
		return false
	}

	// Retire it before the server's 30s idle timeout.
	_ = warm.SetWriteDeadline(time.Time{})
	_ = warm.SetReadDeadline(time.Now().Add(20 * time.Second))

	return true
}

// watch keeps the spare connection alive while wanted and discards it once the server closes it.
func (d *remoteProxyDialer) watch(key string, warm *remoteProxyWarmConn) {
	for {
		// Nothing arrives on an idle connection, so a read only returns when it expires, is unusable or handed out.
		_, err := warm.Read(make([]byte, 1))

		d.mu.Lock()

		if warm.taken {
			warm.dead = !errors.Is(err, os.ErrDeadlineExceeded)
			close(warm.done)
			d.mu.Unlock()

			return
		}

		d.conns[key] = slices.DeleteFunc(d.conns[key], func(c *remoteProxyWarmConn) bool { return c == warm })

		// Keep it while the pool is still wanted at this size, otherwise let it go.
		keep := errors.Is(err, os.ErrDeadlineExceeded) && len(d.conns[key]) < d.size() && time.Since(d.lastUse) < time.Minute
		if keep {
			d.filling[key]++
		}

		d.mu.Unlock()

		if keep && d.keepAlive(warm) {
			d.mu.Lock()
			d.filling[key]--
			d.conns[key] = append(d.conns[key], warm)
			d.mu.Unlock()

			continue
		}

		close(warm.done)
		_ = warm.Close()

		d.mu.Lock()
		if keep {
			d.filling[key]--
		}

		d.mu.Unlock()

		// Replace it so the pool stays warm while the proxy is in use.
		if keep {
			go d.fill(warm.network, warm.addr)
		}

		return
	}
}
