package incus

import (
	"context"
	"crypto/rand"
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
)

// setupOIDCClient initializes the OIDC (OpenID Connect) client with given tokens if it hasn't been set up already.
// It also assigns the protocol's http client to the oidcClient's httpClient.
func (r *ProtocolIncus) setupOIDCClient(token *oidc.Tokens[*oidc.IDTokenClaims]) {
	if r.oidcClient != nil {
		return
	}

	r.oidcClient = newOIDCClient(token)
	r.oidcClient.httpClient = r.http
}

// Custom transport that modifies requests to inject the audience field.
type oidcTransport struct {
	deviceAuthorizationEndpoint string
	audience                    string
}

// oidcTransport is a custom HTTP transport that injects the audience field into requests directed at the device authorization endpoint.
// RoundTrip is a method of oidcTransport that modifies the request, adds the audience parameter if appropriate, and sends it along.
func (o *oidcTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Don't modify the request if it's not to the device authorization endpoint, or there are no
	// URL parameters which need to be set.
	if r.URL.String() != o.deviceAuthorizationEndpoint || len(o.audience) == 0 {
		return http.DefaultTransport.RoundTrip(r)
	}

	err := r.ParseForm()
	if err != nil {
		return nil, err
	}

	if o.audience != "" {
		r.Form.Add("audience", o.audience)
	}

	// Update the body with the new URL parameters.
	body := r.Form.Encode()
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))

	return http.DefaultTransport.RoundTrip(r)
}

var errRefreshAccessToken = fmt.Errorf("Failed refreshing access token")
var oidcScopes = []string{oidc.ScopeOpenID, oidc.ScopeOfflineAccess, oidc.ScopeEmail}

type oidcClient struct {
	httpClient    *http.Client
	oidcTransport *oidcTransport
	tokens        *oidc.Tokens[*oidc.IDTokenClaims]
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

	if issuer == "" || clientID == "" {
		return resp, nil
	}

	err = o.refresh(issuer, clientID)
	if err != nil {
		err = o.authenticate(issuer, clientID, audience)
		if err != nil {
			return nil, err
		}
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

	if issuer == "" || clientID == "" {
		return nil, resp, err
	}

	err = o.refresh(issuer, clientID)
	if err != nil {
		err = o.authenticate(issuer, clientID, audience)
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
func (o *oidcClient) getProvider(issuer string, clientID string) (rp.RelyingParty, error) {
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

	provider, err := rp.NewRelyingPartyOIDC(context.TODO(), issuer, clientID, "", "", oidcScopes, options...)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

// refresh attempts to refresh the OpenID Connect access token for the client using the refresh token.
// If no token is present or the refresh token is empty, it returns an error. If successful, it updates the access token and other relevant token fields.
func (o *oidcClient) refresh(issuer string, clientID string) error {
	if o.tokens.Token == nil || o.tokens.RefreshToken == "" {
		return errRefreshAccessToken
	}

	provider, err := o.getProvider(issuer, clientID)
	if err != nil {
		return errRefreshAccessToken
	}

	oauthTokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](context.TODO(), provider, o.tokens.RefreshToken, "", "")
	if err != nil {
		return errRefreshAccessToken
	}

	o.tokens.Token.AccessToken = oauthTokens.AccessToken
	o.tokens.TokenType = oauthTokens.TokenType
	o.tokens.Expiry = oauthTokens.Expiry

	if oauthTokens.RefreshToken != "" {
		o.tokens.Token.RefreshToken = oauthTokens.RefreshToken
	}

	return nil
}

// authenticate initiates the OpenID Connect device flow authentication process for the client.
// It presents a user code for the end user to input in the device that has web access and waits for them to complete the authentication,
// subsequently updating the client's tokens upon successful authentication.
func (o *oidcClient) authenticate(issuer string, clientID string, audience string) error {
	// Store the old transport and restore it in the end.
	oldTransport := o.httpClient.Transport
	o.oidcTransport.audience = audience
	o.httpClient.Transport = o.oidcTransport

	defer func() {
		o.httpClient.Transport = oldTransport
	}()

	provider, err := o.getProvider(issuer, clientID)
	if err != nil {
		return err
	}

	o.oidcTransport.deviceAuthorizationEndpoint = provider.GetDeviceAuthorizationEndpoint()

	resp, err := rp.DeviceAuthorization(context.TODO(), oidcScopes, provider, nil)
	if err != nil {
		return err
	}

	u, _ := url.Parse(resp.VerificationURIComplete)

	fmt.Printf("URL: %s\n", u.String())
	fmt.Printf("Code: %s\n\n", resp.UserCode)

	_ = openBrowser(u.String())

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
	o.tokens.Token.AccessToken = token.AccessToken
	o.tokens.TokenType = token.TokenType

	if token.RefreshToken != "" {
		o.tokens.Token.RefreshToken = token.RefreshToken
	}

	return nil
}
