package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/auth/common"
	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// testCache returns a certificate cache seeded with the given certificates.
func testCache(certs ...*api.Certificate) *certificate.Cache {
	c := &certificate.Cache{}
	c.SetCertificates(certs)
	return c
}

// TestClassify checks that classify maps a request's details to the expected client class.
func TestClassify(t *testing.T) {
	cache := testCache(
		&api.Certificate{CertificatePut: api.CertificatePut{Type: "client"}, Fingerprint: "unrestricted"},
		&api.Certificate{CertificatePut: api.CertificatePut{Type: "client", Restricted: true, Projects: []string{"p1"}}, Fingerprint: "restricted"},
	)

	rt, err := NewRouter(context.Background(), logger.Log, cache)
	require.NoError(t, err)

	cases := []struct {
		name     string
		protocol string
		username string
		expected clientClass
	}{
		{"unix", "unix", "", clientClassUnix},
		{"tls-unrestricted", api.AuthenticationMethodTLS, "unrestricted", clientClassTLS},
		{"tls-restricted", api.AuthenticationMethodTLS, "restricted", clientClassTLSRestricted},
		{"tls-unknown", api.AuthenticationMethodTLS, "ghost", clientClassTLSRestricted},
		{"oidc", api.AuthenticationMethodOIDC, "", clientClassOIDC},
		{"unknown", "other", "", clientClassDefault},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			details := &requestDetails{RequestDetails: common.RequestDetails{Protocol: c.protocol, Username: c.username}}
			assert.Equal(t, c.expected, rt.classify(details))
		})
	}
}

// TestClassifyUnknownCertificate checks that a fingerprint absent from the trust store only
// classifies as unrestricted when it could have been trusted through the CA.
func TestClassifyUnknownCertificate(t *testing.T) {
	cases := []struct {
		name          string
		trustCA       bool
		serverCA      bool
		expectedClass clientClass
		canEdit       bool
	}{
		{"no-ca", false, false, clientClassTLSRestricted, false},
		{"trust-only", true, false, clientClassTLSRestricted, false},
		{"ca-only", false, true, clientClassTLSRestricted, false},
		{"ca-trusted", true, true, clientClassTLS, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("INCUS_DIR", dir)
			if c.serverCA {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "server.ca"), []byte("ca"), 0o600))
			}

			rt, err := NewRouter(context.Background(), logger.Log, testCache())
			require.NoError(t, err)

			err = rt.Configure(nil, nil, c.trustCA)
			require.NoError(t, err)

			details := &requestDetails{RequestDetails: common.RequestDetails{Protocol: api.AuthenticationMethodTLS, Username: "ghost"}}
			assert.Equal(t, c.expectedClass, rt.classify(details))

			ctx := context.WithValue(context.Background(), request.CtxUsername, "ghost")
			ctx = context.WithValue(ctx, request.CtxProtocol, api.AuthenticationMethodTLS)
			r, err := http.NewRequestWithContext(ctx, http.MethodPatch, "/1.0", nil)
			require.NoError(t, err)

			err = rt.CheckPermission(ctx, r, ObjectServer(), EntitlementCanEdit)
			if c.canEdit {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestFanout checks that fanout invokes fn once per loaded driver and joins their errors.
func TestFanout(t *testing.T) {
	rt, err := NewRouter(context.Background(), logger.Log, &certificate.Cache{})
	require.NoError(t, err)

	drivers := rt.state.Load().drivers

	// fn runs once for every loaded driver; no errors joins to nil.
	var count int
	err = rt.fanout(func(a Authorizer) error {
		count++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, len(drivers), count)

	// Errors returned by drivers are joined into the result.
	boom := errors.New("boom")
	err = rt.fanout(func(a Authorizer) error { return boom })
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}
