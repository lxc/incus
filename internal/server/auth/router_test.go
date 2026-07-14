package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/auth/common"
	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// TestClassify checks that classify maps a request's details to the expected client class.
func TestClassify(t *testing.T) {
	rt, err := NewRouter(context.Background(), logger.Log, &certificate.Cache{})
	require.NoError(t, err)

	cases := []struct {
		name     string
		protocol string
		expected clientClass
	}{
		{"unix", "unix", clientClassUnix},
		{"tls", api.AuthenticationMethodTLS, clientClassTLS},
		{"oidc", api.AuthenticationMethodOIDC, clientClassOIDC},
		{"unknown", "other", clientClassDefault},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			details := &requestDetails{RequestDetails: common.RequestDetails{Protocol: c.protocol}}
			assert.Equal(t, c.expected, rt.classify(details))
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
