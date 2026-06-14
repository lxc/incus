package ws

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// trustedOriginsMu protects access to trustedOrigins.
var trustedOriginsMu sync.RWMutex

// trustedOrigins is the list of origins accepted on top of same-origin requests.
var trustedOrigins []string

// SetTrustedOrigins sets the origins accepted during websocket upgrades ("*" allows any).
func SetTrustedOrigins(origins []string) {
	trusted := make([]string, 0, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}

		trusted = append(trusted, origin)
	}

	trustedOriginsMu.Lock()
	defer trustedOriginsMu.Unlock()

	trustedOrigins = trusted
}

// checkOrigin allows requests with no Origin, same-origin requests or trusted origins.
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	// Same-origin requests are always allowed.
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}

	// Check against the configured trusted origins.
	trustedOriginsMu.RLock()
	defer trustedOriginsMu.RUnlock()

	for _, trusted := range trustedOrigins {
		if trusted == "*" {
			return true
		}

		// Match the full origin, the host (with port) or the hostname (without port).
		if strings.EqualFold(trusted, origin) || strings.EqualFold(trusted, u.Host) || strings.EqualFold(trusted, u.Hostname()) {
			return true
		}
	}

	return false
}

// Upgrader is a websocket upgrader which validates the request Origin.
var Upgrader = websocket.Upgrader{
	HandshakeTimeout: time.Second * 5,
	CheckOrigin:      checkOrigin,
}
