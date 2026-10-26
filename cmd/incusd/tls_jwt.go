package main

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/internal/server/response"
	localUtil "github.com/lxc/incus/v7/internal/server/util"
)

// tlsJWTCookie returns the cookie carrying a TLS JWT, an empty token clears it.
func tlsJWTCookie(token string, expiry *time.Time) *http.Cookie {
	cookie := &http.Cookie{
		Name:     "tls_jwt",
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}

	if token == "" {
		cookie.Expires = time.Unix(0, 0)
	} else if expiry != nil {
		cookie.Expires = *expiry
	}

	return cookie
}

// tlsJWTLogin validates the TLS JWT from the Authorization header and stores it in a cookie.
func (d *Daemon) tlsJWTLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = response.ErrorResponse(http.StatusMethodNotAllowed, "Only POST is supported").Render(w)
		return
	}

	// Protect against CSRF when using UI with browser that supports Fetch metadata.
	if slices.Contains([]string{"cross-site", "same-site"}, r.Header.Get("Sec-Fetch-Site")) {
		_ = response.ErrorResponse(http.StatusForbidden, "Forbidden Sec-Fetch-Site header value").Render(w)
		return
	}

	if r.TLS == nil {
		_ = response.BadRequest(errors.New("TLS JWT login requires a TLS connection")).Render(w)
		return
	}

	token := localUtil.BearerToken(r)
	if token == "" {
		_ = response.BadRequest(errors.New("Missing bearer token")).Render(w)
		return
	}

	trustedCerts, err := d.getTrustedCertificates()
	if err != nil {
		_ = response.SmartError(err).Render(w)
		return
	}

	// Apply the same validation as a regular request carrying the token.
	cert, expiry, err := localUtil.VerifyJwtToken(token, trustedCerts[certificate.TypeClient])
	if err != nil {
		_ = response.Forbidden(errors.New("Invalid token")).Render(w)
		return
	}

	trusted, _ := localUtil.CheckTrustState(*cert, trustedCerts[certificate.TypeClient], d.endpoints.NetworkCert(), d.globalConfig.TrustCACertificates())
	if !trusted {
		_ = response.Forbidden(errors.New("Invalid token")).Render(w)
		return
	}

	http.SetCookie(w, tlsJWTCookie(token, expiry))
	_ = response.EmptySyncResponse.Render(w)
}

// tlsJWTLogout clears the TLS JWT cookie.
func (d *Daemon) tlsJWTLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, tlsJWTCookie("", nil))
	_ = response.EmptySyncResponse.Render(w)
}
