package incus

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"golang.org/x/oauth2"

	"github.com/lxc/incus/v7/shared/util"
)

// ErrOIDCExpired is returned when the token is expired and we can't retry the request ourselves.
var ErrOIDCExpired = errors.New("OIDC token expired, please re-try the request")

// setupOIDCClient initializes the OIDC (OpenID Connect) client with given tokens if it hasn't been set up already.
// It also assigns the protocol's http client to the oidcClient's httpClient.
func (r *ProtocolIncus) setupOIDCClient(token *oidc.Tokens[*oidc.IDTokenClaims], skipAuthenticate bool) {
	if r.oidcClient != nil {
		return
	}

	r.oidcClient = newOIDCClient(token)
	r.oidcClient.skipAuthenticate = skipAuthenticate
	r.oidcClient.httpClient = r.http

	// Route requests to the server through its own transport and everything else (the provider) through the default one.
	r.oidcClient.oidcTransport.base = r.http.Transport
	r.oidcClient.oidcTransport.serverHost = r.httpBaseURL.Host
	r.http.Transport = r.oidcClient.oidcTransport
}

// GetOIDCTokens returns the current OIDC tokens (if any) from the OIDC client.
//
// This should only be used by internal Incus tools when it's not possible to get the tokens from a Config struct.
func (r *ProtocolIncus) GetOIDCTokens() *oidc.Tokens[*oidc.IDTokenClaims] {
	if r.oidcClient == nil {
		return nil
	}

	return r.oidcClient.tokens
}

// oidcTransport is a custom HTTP transport that sends requests to the Incus server through the server-specific transport
// and requests to the OIDC provider through the default transport, adjusting the form parameters of the device authorization
// and token endpoint requests.
type oidcTransport struct {
	base                        http.RoundTripper
	serverHost                  string
	deviceAuthorizationEndpoint string
	tokenEndpoint               string
	audience                    string
}

// Transport returns the underlying *http.Transport used for the Incus server.
func (o *oidcTransport) Transport() *http.Transport {
	switch t := o.base.(type) {
	case *http.Transport:
		return t
	case HTTPTransporter:
		return t.Transport()
	default:
		return nil
	}
}

// RoundTrip is a method of oidcTransport that modifies the request, adds the audience parameter if appropriate, and sends it along.
func (o *oidcTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Requests to the Incus server use the server-specific transport.
	if o.base != nil && r.URL.Host == o.serverHost {
		return o.base.RoundTrip(r)
	}

	isDeviceAuthorization := r.URL.String() == o.deviceAuthorizationEndpoint
	isToken := r.URL.String() == o.tokenEndpoint

	// Don't modify requests to other endpoints.
	if !isDeviceAuthorization && !isToken {
		return http.DefaultTransport.RoundTrip(r)
	}

	err := r.ParseForm()
	if err != nil {
		return nil, err
	}

	if isDeviceAuthorization && o.audience != "" {
		r.Form.Add("audience", o.audience)
	}

	// Drop empty parameters as some providers reject an empty client_secret.
	for key, values := range r.Form {
		if strings.Join(values, "") == "" {
			delete(r.Form, key)
		}
	}

	// Update the body with the new URL parameters.
	body := r.Form.Encode()
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))

	return http.DefaultTransport.RoundTrip(r)
}

var errRefreshAccessToken = errors.New("Failed refreshing access token")

type oidcClient struct {
	httpClient       *http.Client
	oidcTransport    *oidcTransport
	tokens           *oidc.Tokens[*oidc.IDTokenClaims]
	skipAuthenticate bool
}

// oidcClient is a structure encapsulating an HTTP client, OIDC transport, and a token for OpenID Connect (OIDC) operations.
// newOIDCClient constructs a new oidcClient, ensuring the token field is non-nil to prevent panics during authentication.
func newOIDCClient(tokens *oidc.Tokens[*oidc.IDTokenClaims]) *oidcClient {
	client := oidcClient{
		tokens:        tokens,
		httpClient:    &http.Client{},
		oidcTransport: &oidcTransport{},
	}

	// Ensure client.tokens is never nil otherwise authenticate() will panic.
	if client.tokens == nil {
		client.tokens = &oidc.Tokens[*oidc.IDTokenClaims]{}
	}

	return &client
}

// getAccessToken returns the Access Token from the oidcClient's tokens, or an empty string if no tokens are present.
func (o *oidcClient) getAccessToken() string {
	if o.tokens == nil || o.tokens.Token == nil {
		return ""
	}

	return o.tokens.AccessToken
}

// do function executes an HTTP request using the oidcClient's http client, and manages authorization by refreshing or authenticating as needed.
// If the request fails with an HTTP Unauthorized status, it attempts to refresh the access token, or perform an OIDC authentication if refresh fails.
func (o *oidcClient) do(req *http.Request) (*http.Response, error) {
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Return immediately if the error is not HTTP status unauthorized.
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	issuer := resp.Header.Get("X-Incus-OIDC-issuer")
	clientID := resp.Header.Get("X-Incus-OIDC-clientid")
	audience := resp.Header.Get("X-Incus-OIDC-audience")
	scopes := resp.Header.Get("X-Incus-OIDC-scopes")
	if scopes == "" {
		scopes = "openid,offline_access"
	}

	if issuer == "" || clientID == "" {
		return resp, nil
	}

	// Refresh the token.
	err = o.refresh(issuer, clientID, scopes)
	if err != nil {
		if o.skipAuthenticate {
			return nil, fmt.Errorf("Authentication not found or expired: %w", err)
		}

		err = o.authenticate(issuer, clientID, audience, scopes)
		if err != nil {
			return nil, err
		}
	}

	// If not dealing with something we can retry, return a clear error.
	if req.Method != "GET" && req.GetBody == nil {
		return resp, ErrOIDCExpired
	}

	// Set the new access token in the header.
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.tokens.AccessToken))

	// Reset the request body.
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}

		req.Body = body
	}

	resp, err = o.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// dial function executes a websocket request and handles OIDC authentication and refresh.
func (o *oidcClient) dial(dialer websocket.Dialer, uri string, req *http.Request) (*websocket.Conn, *http.Response, error) {
	conn, resp, err := dialer.Dial(uri, req.Header)
	if err != nil && resp == nil {
		return nil, nil, err
	}

	// Return immediately if the error is not HTTP status unauthorized.
	if conn != nil && resp.StatusCode != http.StatusUnauthorized {
		return conn, resp, nil
	}

	issuer := resp.Header.Get("X-Incus-OIDC-issuer")
	clientID := resp.Header.Get("X-Incus-OIDC-clientid")
	audience := resp.Header.Get("X-Incus-OIDC-audience")
	scopes := resp.Header.Get("X-Incus-OIDC-scopes")
	if scopes == "" {
		scopes = "openid,offline_access"
	}

	if issuer == "" || clientID == "" {
		return nil, resp, err
	}

	err = o.refresh(issuer, clientID, scopes)
	if err != nil {
		if o.skipAuthenticate {
			return nil, resp, fmt.Errorf("Authentication not found or expired: %w", err)
		}

		err = o.authenticate(issuer, clientID, audience, scopes)
		if err != nil {
			return nil, resp, err
		}
	}

	// Set the new access token in the header.
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.tokens.AccessToken))

	return dialer.Dial(uri, req.Header)
}

// getProvider initializes a new OpenID Connect Relying Party for a given issuer and clientID.
// The function also creates a secure CookieHandler with random encryption and hash keys, and applies a series of configurations on the Relying Party.
func (o *oidcClient) getProvider(issuer string, clientID string, scopes string) (rp.RelyingParty, error) {
	hashKey := make([]byte, 16)
	encryptKey := make([]byte, 16)

	_, err := rand.Read(hashKey)
	if err != nil {
		return nil, err
	}

	_, err = rand.Read(encryptKey)
	if err != nil {
		return nil, err
	}

	cookieHandler := httphelper.NewCookieHandler(hashKey, encryptKey, httphelper.WithUnsecure())
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
		rp.WithHTTPClient(o.httpClient),
	}

	provider, err := rp.NewRelyingPartyOIDC(context.TODO(), issuer, clientID, "", "", strings.Split(scopes, ","), options...)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

// refresh attempts to refresh the OpenID Connect access token for the client using the refresh token.
// If no token is present or the refresh token is empty, it returns an error. If successful, it updates the access token and other relevant token fields.
func (o *oidcClient) refresh(issuer string, clientID string, scopes string) error {
	if o.tokens.Token == nil || o.tokens.RefreshToken == "" {
		return errRefreshAccessToken
	}

	provider, err := o.getProvider(issuer, clientID, scopes)
	if err != nil {
		return errRefreshAccessToken
	}

	o.oidcTransport.tokenEndpoint = provider.OAuthConfig().Endpoint.TokenURL

	oauthTokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](context.TODO(), provider, o.tokens.RefreshToken, "", "")
	if err != nil {
		return errRefreshAccessToken
	}

	o.tokens.AccessToken = oauthTokens.AccessToken
	o.tokens.TokenType = oauthTokens.TokenType
	o.tokens.Expiry = oauthTokens.Expiry

	if oauthTokens.RefreshToken != "" {
		o.tokens.RefreshToken = oauthTokens.RefreshToken
	}

	return nil
}

// authenticate initiates the OpenID Connect device flow authentication process for the client.
// It presents a user code for the end user to input in the device that has web access and waits for them to complete the authentication,
// subsequently updating the client's tokens upon successful authentication.
func (o *oidcClient) authenticate(issuer string, clientID string, audience string, scopes string) error {
	o.oidcTransport.audience = audience

	provider, err := o.getProvider(issuer, clientID, scopes)
	if err != nil {
		return err
	}

	o.oidcTransport.deviceAuthorizationEndpoint = provider.GetDeviceAuthorizationEndpoint()
	o.oidcTransport.tokenEndpoint = provider.OAuthConfig().Endpoint.TokenURL

	resp, err := rp.DeviceAuthorization(context.TODO(), strings.Split(scopes, ","), provider, nil)
	if err != nil {
		return err
	}

	u, _ := url.Parse(resp.VerificationURIComplete)

	fmt.Printf("URL: %s\n", u.String())
	fmt.Printf("Code: %s\n\n", resp.UserCode)

	_ = util.OpenBrowser(u.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT)
	defer stop()

	token, err := rp.DeviceAccessToken(ctx, resp.DeviceCode, time.Duration(resp.Interval)*time.Second, provider)
	if err != nil {
		return err
	}

	if o.tokens.Token == nil {
		o.tokens.Token = &oauth2.Token{}
	}

	o.tokens.Expiry = time.Now().Add(time.Duration(token.ExpiresIn))
	o.tokens.IDToken = token.IDToken
	o.tokens.AccessToken = token.AccessToken
	o.tokens.TokenType = token.TokenType

	if token.RefreshToken != "" {
		o.tokens.RefreshToken = token.RefreshToken
	}

	return nil
}
