//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
)

func TestGetCertificate(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	_, err := cluster.CreateCertificate(ctx, tx.Tx(), cluster.Certificate{Fingerprint: "foobar"})
	require.NoError(t, err)

	cert, err := cluster.GetCertificate(ctx, tx.Tx(), "foobar")
	require.NoError(t, err)
	assert.Equal(t, cert.Fingerprint, "foobar")
}
