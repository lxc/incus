package candid

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/candid/candidclient"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/identchecker"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"

	"github.com/canonical/lxd/lxd/response"
)

// Verifier contains everything needed to verify a request.
type Verifier struct {
	endpoint string
	expiry   int64
	bakery   *identchecker.Bakery
}

// Auth performs the candid authentication of the request.
func (v *Verifier) Auth(r *http.Request) (*identchecker.AuthInfo, error) {
	// Validate external authentication.
	ctx := httpbakery.ContextWithRequest(context.TODO(), r)
	authChecker := v.bakery.Checker.Auth(httpbakery.RequestMacaroons(r)...)

	ops := []bakery.Op{{
		Entity: r.URL.Path,
		Action: r.Method,
	}}

	return authChecker.Allow(ctx, ops...)
}

// IsRequest checks if the request is using Candid authentication.
func (v *Verifier) IsRequest(r *http.Request) bool {
	return r.Header.Get(httpbakery.BakeryProtocolHeader) != ""
}

// WriteRequest writes the required response to trigger Candid authentication.
func (v *Verifier) WriteRequest(r *http.Request, w http.ResponseWriter, derr *bakery.DischargeRequiredError) {
	ctx := httpbakery.ContextWithRequest(context.TODO(), r)
	caveats := append(derr.Caveats,
		checkers.TimeBeforeCaveat(time.Now().Add(time.Duration(v.expiry)*time.Second)))

	// Mint an appropriate macaroon and send it back to the client.
	m, err := v.bakery.Oven.NewMacaroon(
		ctx, httpbakery.RequestVersion(r), caveats, derr.Ops...)
	if err != nil {
		resp := response.ErrorResponse(http.StatusInternalServerError, err.Error())
		_ = resp.Render(w)
		return
	}

	herr := httpbakery.NewDischargeRequiredError(
		httpbakery.DischargeRequiredErrorParams{
			Macaroon:      m,
			OriginalError: derr,
			Request:       r,
		})
	herr.(*httpbakery.Error).Info.CookieNameSuffix = "auth"
	httpbakery.WriteError(ctx, w, herr)
}

// NewVerifier returns a new Candid verifier.
func NewVerifier(authEndpoint string, authPubkey string, expiry int64, domains string) (*Verifier, error) {
	// Parse the list of domains
	authDomains := []string{}
	for _, domain := range strings.Split(domains, ",") {
		if domain == "" {
			continue
		}

		authDomains = append(authDomains, strings.TrimSpace(domain))
	}

	// Allow disable external authentication
	if authEndpoint == "" {
		return nil, nil
	}

	// Setup the candid client
	idmClient, err := candidclient.New(candidclient.NewParams{
		BaseURL: authEndpoint,
	})
	if err != nil {
		return nil, err
	}

	idmClientWrapper := &IdentityClientWrapper{
		client:       idmClient,
		ValidDomains: authDomains,
	}

	// Generate an internal private key
	key, err := bakery.GenerateKey()
	if err != nil {
		return nil, err
	}

	pkCache := bakery.NewThirdPartyStore()
	pkLocator := httpbakery.NewThirdPartyLocator(nil, pkCache)
	if authPubkey != "" {
		// Parse the public key
		pkKey := bakery.Key{}
		err := pkKey.UnmarshalText([]byte(authPubkey))
		if err != nil {
			return nil, err
		}

		// Add the key information
		pkCache.AddInfo(authEndpoint, bakery.ThirdPartyInfo{
			PublicKey: bakery.PublicKey{Key: pkKey},
			Version:   3,
		})

		// Allow http URLs if we have a public key set
		if strings.HasPrefix(authEndpoint, "http://") {
			pkLocator.AllowInsecure()
		}
	}

	// Setup the bakery
	bakery := identchecker.NewBakery(identchecker.BakeryParams{
		Key:            key,
		Location:       authEndpoint,
		Locator:        pkLocator,
		Checker:        httpbakery.NewChecker(),
		IdentityClient: idmClientWrapper,
		Authorizer: identchecker.ACLAuthorizer{
			GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
				return []string{identchecker.Everyone}, false, nil
			},
		},
	})

	// Setup the verifier.
	verifier := &Verifier{
		endpoint: authEndpoint,
		expiry:   expiry,
		bakery:   bakery,
	}

	return verifier, nil
}
