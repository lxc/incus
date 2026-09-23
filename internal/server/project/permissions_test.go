package project_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// If there's no limit configured on the project, the check passes.
func TestAllowInstanceCreation_NotConfigured(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	req := api.InstancesPost{
		Name: "c1",
		Type: api.InstanceTypeContainer,
	}

	err := project.AllowInstanceCreation(tx, "default", req)
	assert.NoError(t, err)
}

// If a limit is configured and the current number of instances is below it, the check passes.
func TestAllowInstanceCreation_Below(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "5"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.NoError(t, err)
}

// If a limit is configured and it matches the current number of instances, the
// check fails.
func TestAllowInstanceCreation_Above(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.EqualError(t, err, `Reached maximum number of instances of type "container" in project "p1"`)
}

// If a limit is configured, but for a different instance type, the check
// passes.
func TestAllowInstanceCreation_DifferentType(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
		Project:      "p1",
		Name:         "vm1",
		Type:         instancetype.VM,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.NoError(t, err)
}

// If a limit is configured, but the limit on instances is more
// restrictive, the check fails.
func TestAllowInstanceCreation_AboveInstances(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "5", "limits.instances": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.EqualError(t, err, `Reached maximum number of instances in project "p1"`)
}

// If the recorded instances fit within the limit, the post-insert check passes.
func TestCheckLimits_AtLimit(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.instances": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	err = project.CheckLimits(tx, "p1")
	assert.NoError(t, err)
}

// If more instances got recorded than the limit allows, the post-insert check fails.
func TestCheckLimits_AboveInstances(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.instances": "1"})
	require.NoError(t, err)

	for _, name := range []string{"c1", "c2"} {
		_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
			Project:      "p1",
			Name:         name,
			Type:         instancetype.Container,
			Architecture: 1,
			Node:         "none",
		})
		require.NoError(t, err)
	}

	err = project.CheckLimits(tx, "p1")
	assert.EqualError(t, err, `Reached maximum number of instances in project "p1"`)
}

// A profile with a forbidden low-level key can't be created in a restricted project.
func TestAllowProfileCreation_Restricted(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "true"})
	require.NoError(t, err)

	req := api.ProfilesPost{
		Name: "evil",
		ProfilePut: api.ProfilePut{
			Config: map[string]string{"raw.lxc": "lxc.hook.pre-start=/bin/true"},
		},
	}

	err = project.AllowProfileCreation(tx, "p1", req)
	assert.ErrorContains(t, err, `Use of low-level config "raw.lxc" on profile "evil"`)

	req.Config = map[string]string{"limits.cpu": "1"}
	err = project.AllowProfileCreation(tx, "p1", req)
	assert.NoError(t, err)
}

// If a direct targeting is blocked, the check fails.
func TestCheckClusterTargetRestriction_RestrictedTrue(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "true", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	dbProject, err := cluster.GetProject(ctx, tx.Tx(), "p1")
	require.NoError(t, err)

	p, err := dbProject.ToAPI(ctx, tx.Tx())
	require.NoError(t, err)

	req := &http.Request{}
	authorizer, err := auth.LoadAuthorizer(context.Background(), auth.DriverTLS, logger.Log, &certificate.Cache{})
	require.NoError(t, err)

	err = project.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.EqualError(t, err, "This project doesn't allow cluster member targeting")
}

// If a direct targeting is blocked but the user can override it, the check passes.
func TestCheckClusterTargetRestriction_RestrictedTrueWithOverride(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "true", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	dbProject, err := cluster.GetProject(ctx, tx.Tx(), "p1")
	require.NoError(t, err)

	p, err := dbProject.ToAPI(ctx, tx.Tx())
	require.NoError(t, err)

	req := &http.Request{
		URL: &api.NewURL().Path("1.0", "instances").WithQuery("target", "node01").URL,
	}

	req = req.WithContext(context.WithValue(req.Context(), request.CtxProtocol, "tls"))
	req = req.WithContext(context.WithValue(req.Context(), request.CtxUsername, "my-certificate-fingerprint"))
	certificateCache := &certificate.Cache{}

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             now,
		NotAfter:              now.Add(5 * time.Minute),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	require.NoError(t, err)

	// Setting the certificate and not projects means the certificate is not restricted and therefore the user is an
	// admin that can override the cluster target restriction.
	certificateCache.SetCertificates([]*api.Certificate{{
		Fingerprint: "my-certificate-fingerprint",
		CertificatePut: api.CertificatePut{
			Type:        api.CertificateTypeClient,
			Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		},
	}})

	authorizer, err := auth.LoadAuthorizer(context.Background(), auth.DriverTLS, logger.Log, certificateCache)
	require.NoError(t, err)

	err = project.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.Nil(t, err)
}

// If a direct targeting is allowed, the check passes.
func TestCheckClusterTargetRestriction_RestrictedFalse(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "false", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	dbProject, err := cluster.GetProject(ctx, tx.Tx(), "p1")
	require.NoError(t, err)

	p, err := dbProject.ToAPI(ctx, tx.Tx())
	require.NoError(t, err)

	req := &http.Request{}
	authorizer, err := auth.LoadAuthorizer(context.Background(), auth.DriverTLS, logger.Log, &certificate.Cache{})
	require.NoError(t, err)

	err = project.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.NoError(t, err)
}
