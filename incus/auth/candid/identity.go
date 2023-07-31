package candid

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/identchecker"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// IdentityClientWrapper is a wrapper around an IdentityClient.
type IdentityClientWrapper struct {
	client       identchecker.IdentityClient
	ValidDomains []string
}

// IdentityFromContext returns the identity from the preovided context.
func (m *IdentityClientWrapper) IdentityFromContext(ctx context.Context) (identchecker.Identity, []checkers.Caveat, error) {
	return m.client.IdentityFromContext(ctx)
}

// DeclaredIdentity performs a check of the Candid domain before returning the declared identity.
func (m *IdentityClientWrapper) DeclaredIdentity(ctx context.Context, declared map[string]string) (identchecker.Identity, error) {
	// Extract the domain from the username
	fields := strings.SplitN(declared["username"], "@", 2)

	// Only validate domain if we have a list of valid domains
	if len(m.ValidDomains) > 0 {
		// If no domain was provided by candid, reject the request
		if len(fields) < 2 {
			logger.Warnf("Failed candid client authentication: no domain provided")
			return nil, fmt.Errorf("Missing domain in candid reply")
		}

		// Check that it was a valid domain
		if !shared.StringInSlice(fields[1], m.ValidDomains) {
			logger.Warnf("Failed candid client authentication: untrusted domain \"%s\"", fields[1])
			return nil, fmt.Errorf("Untrusted candid domain")
		}
	}

	return m.client.DeclaredIdentity(ctx, declared)
}
