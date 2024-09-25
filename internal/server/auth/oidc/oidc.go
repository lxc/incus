package oidc

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/lxc/incus/v6/shared/util"
)

// Verifier holds all information needed to verify an access token offline.
type Verifier struct {
	accessTokenVerifier *op.AccessTokenVerifier

	clientID  string
	issuer    string
	scopes    []string
	audience  string
	claim     string
	cookieKey []byte
}

// AuthError represents an authentication error.
type AuthError struct {
	Err error
}

func (e AuthError) Error() string {
	return fmt.Sprintf("Failed to authenticate: %s", e.Err.Error())
}

func (e AuthError) Unwrap() error {
	return e.Err
}

// Auth extracts the token, validates it and returns the user information.
func (o *Verifier) Auth(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, error) {
	var token string

	auth := r.Header.Get("Authorization")
	if auth != "" {
		// When a client wants to authenticate, it needs to set the Authorization HTTP header like this:
		//    Authorization Bearer <access_token>
		// If set correctly, Incus will attempt to verify the access token, and grant access if it's valid.
		// If the verification fails, Incus will return an InvalidToken error. The client should then either use its refresh token to get a new valid access token, or log in again.
		// If the Authorization header is missing, Incus returns an AuthenticationRequired error.
		// Both returned errors contain information which are needed for the client to authenticate.
		parts := strings.Split(auth, "Bearer ")
		if len(parts) != 2 {
			return "", &AuthError{fmt.Errorf("Bad authorization token, expected a Bearer token")}
		}

		token = parts[1]
	} else {
		// When not using a Bearer token, fetch the equivalent from a cookie and move on with it.
		cookie, err := r.Cookie("oidc_access")
		if err != nil {
			return "", &AuthError{err}
		}

		token = cookie.Value
	}

	if o.accessTokenVerifier == nil {
		var err error

		o.accessTokenVerifier, err = getAccessTokenVerifier(o.issuer)
		if err != nil {
			return "", &AuthError{err}
		}
	}

	claims, err := o.VerifyAccessToken(ctx, token)
	if err != nil {
		// See if we can refresh the access token.
		cookie, cookieErr := r.Cookie("oidc_refresh")
		if cookieErr != nil {
			return "", &AuthError{err}
		}

		// Get the provider.
		provider, err := o.getProvider(r)
		if err != nil {
			return "", &AuthError{err}
		}

		// Attempt the refresh.
		tokens, err := rp.RefreshTokens[*oidc.IDTokenClaims](context.TODO(), provider, cookie.Value, "", "")
		if err != nil {
			return "", &AuthError{err}
		}

		// Validate the refreshed token.
		claims, err = o.VerifyAccessToken(ctx, tokens.AccessToken)
		if err != nil {
			return "", &AuthError{err}
		}

		// If we have a ResponseWriter, refresh the cookies.
		if w != nil {
			// Update the access token cookie.
			accessCookie := http.Cookie{
				Name:     "oidc_access",
				Value:    tokens.AccessToken,
				Path:     "/",
				Secure:   true,
				HttpOnly: false,
				SameSite: http.SameSiteStrictMode,
			}

			http.SetCookie(w, &accessCookie)

			// Update the refresh token cookie.
			if tokens.RefreshToken != "" {
				refreshCookie := http.Cookie{
					Name:     "oidc_refresh",
					Value:    tokens.RefreshToken,
					Path:     "/",
					Secure:   true,
					HttpOnly: false,
					SameSite: http.SameSiteStrictMode,
				}

				http.SetCookie(w, &refreshCookie)
			}
		}
	}

	if o.claim != "" {
		claim := claims.Claims[o.claim]
		username, ok := claim.(string)
		if claim == nil || !ok || username == "" {
			return "", fmt.Errorf("OIDC user is missing required claim %q", o.claim)
		}

		return username, nil
	}

	user, ok := claims.Claims["email"]
	if ok && user != nil && user.(string) != "" {
		return user.(string), nil
	}

	return claims.Subject, nil
}

func (o *Verifier) Login(w http.ResponseWriter, r *http.Request) {
	// Get the provider.
	provider, err := o.getProvider(r)
	if err != nil {
		return
	}

	handler := rp.AuthURLHandler(func() string { return uuid.New().String() }, provider, rp.WithURLParam("audience", o.audience))
	handler(w, r)
}

func (o *Verifier) Logout(w http.ResponseWriter, r *http.Request) {
	// Attempt to get the provider.
	provider, _ := o.getProvider(r)

	// Attempt to get the token.
	var token string
	cookie, err := r.Cookie("oidc_id")
	if err == nil {
		token = cookie.Value
	}

	// Attempt to end the OIDC session.
	if provider != nil && token != "" {
		_, _ = rp.EndSession(r.Context(), provider, token, fmt.Sprintf("https://%s", r.Host), "")
	}

	// Access token.
	accessCookie := http.Cookie{
		Name:     "oidc_access",
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
	}

	http.SetCookie(w, &accessCookie)

	// ID token.
	idCookie := http.Cookie{
		Name:     "oidc_id",
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
	}

	http.SetCookie(w, &idCookie)

	// Refresh token.
	refreshCookie := http.Cookie{
		Name:     "oidc_refresh",
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
	}

	http.SetCookie(w, &refreshCookie)
}

func (o *Verifier) Callback(w http.ResponseWriter, r *http.Request) {
	// Get the provider.
	provider, err := o.getProvider(r)
	if err != nil {
		return
	}

	handler := rp.CodeExchangeHandler(func(w http.ResponseWriter, r *http.Request, tokens *oidc.Tokens[*oidc.IDTokenClaims], state string, rp rp.RelyingParty) {
		// Access token.
		accessCookie := http.Cookie{
			Name:     "oidc_access",
			Value:    tokens.AccessToken,
			Path:     "/",
			Secure:   true,
			HttpOnly: false,
			SameSite: http.SameSiteStrictMode,
		}

		http.SetCookie(w, &accessCookie)

		// Refresh token.
		if tokens.RefreshToken != "" {
			refreshCookie := http.Cookie{
				Name:     "oidc_refresh",
				Value:    tokens.RefreshToken,
				Path:     "/",
				Secure:   true,
				HttpOnly: false,
				SameSite: http.SameSiteStrictMode,
			}

			http.SetCookie(w, &refreshCookie)
		}

		// ID token.
		if tokens.IDToken != "" {
			idCookie := http.Cookie{
				Name:     "oidc_id",
				Value:    tokens.IDToken,
				Path:     "/",
				Secure:   true,
				HttpOnly: false,
				SameSite: http.SameSiteStrictMode,
			}

			http.SetCookie(w, &idCookie)
		}

		// Send to the UI.
		// NOTE: Once the UI does the redirection on its own, we may be able to use the referer here instead.
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	}, provider)

	handler(w, r)
}

// VerifyAccessToken is a wrapper around op.VerifyAccessToken which avoids having to deal with Go generics elsewhere. It validates the access token (issuer, signature and expiration).
func (o *Verifier) VerifyAccessToken(ctx context.Context, token string) (*oidc.AccessTokenClaims, error) {
	var err error

	if o.accessTokenVerifier == nil {
		o.accessTokenVerifier, err = getAccessTokenVerifier(o.issuer)
		if err != nil {
			return nil, err
		}
	}

	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, token, o.accessTokenVerifier)
	if err != nil {
		return nil, err
	}

	// Check that the token includes the configured audience.
	audience := claims.GetAudience()
	if o.audience != "" && !slices.Contains(audience, o.audience) {
		return nil, fmt.Errorf("Provided OIDC token doesn't allow the configured audience")
	}

	return claims, nil
}

// WriteHeaders writes the OIDC configuration as HTTP headers so the client can initatiate the device code flow.
func (o *Verifier) WriteHeaders(w http.ResponseWriter) error {
	w.Header().Set("X-Incus-OIDC-issuer", o.issuer)
	w.Header().Set("X-Incus-OIDC-clientid", o.clientID)
	w.Header().Set("X-Incus-OIDC-audience", o.audience)

	return nil
}

// IsRequest checks if the request is using OIDC authentication.
func (o *Verifier) IsRequest(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}

	cookie, err := r.Cookie("oidc_access")
	if err == nil && cookie != nil {
		return true
	}

	return false
}

func (o *Verifier) getProvider(r *http.Request) (rp.RelyingParty, error) {
	cookieHandler := httphelper.NewCookieHandler(o.cookieKey, o.cookieKey, httphelper.WithUnsecure())
	options := []rp.Option{
		rp.WithCookieHandler(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5 * time.Second)),
		rp.WithPKCE(cookieHandler),
	}

	provider, err := rp.NewRelyingPartyOIDC(context.TODO(), o.issuer, o.clientID, "", fmt.Sprintf("https://%s/oidc/callback", r.Host), o.scopes, options...)
	if err != nil {
		return nil, err
	}

	return provider, nil
}

// getAccessTokenVerifier calls the OIDC discovery endpoint in order to get the issuer's remote keys which are needed to create an access token verifier.
func getAccessTokenVerifier(issuer string) (*op.AccessTokenVerifier, error) {
	discoveryConfig, err := client.Discover(context.TODO(), issuer, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("Failed calling OIDC discovery endpoint: %w", err)
	}

	keySet := rp.NewRemoteKeySet(http.DefaultClient, discoveryConfig.JwksURI)

	return op.NewAccessTokenVerifier(issuer, keySet), nil
}

// NewVerifier returns a Verifier.
func NewVerifier(issuer string, clientid string, scope string, audience string, claim string) (*Verifier, error) {
	cookieKey, err := uuid.New().MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("Failed to create UUID: %w", err)
	}

	scopes := util.SplitNTrimSpace(scope, ",", -1, false)
	verifier := &Verifier{issuer: issuer, clientID: clientid, scopes: scopes, audience: audience, cookieKey: cookieKey, claim: claim}
	verifier.accessTokenVerifier, _ = getAccessTokenVerifier(issuer)

	return verifier, nil
}
