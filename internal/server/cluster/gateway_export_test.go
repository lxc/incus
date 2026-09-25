package cluster

import (
	"context"
	"crypto/x509"

	"github.com/cowsql/go-cowsql/cluster"
	cowsqldb "github.com/cowsql/go-cowsql/cluster/db"
	"github.com/lxc/incus/v7/internal/server/db"
)

type TrustedCluster struct {
	cowsqldb.Cluster
}

func SetTrustedClusterDB(gateway cluster.Gateway, c *db.Cluster) {
	if c != nil {
		gateway.SetClusterDB(&TrustedCluster{Cluster: cowsqlCluster(c)})
	} else {
		gateway.SetClusterDB(nil)
	}
}

// SetNodeCertificateByName is a wrapper to get past trusted cert validation, as all cluster members use the same cert in tests.
func (c *TrustedCluster) SetNodeCertificateByName(ctx context.Context, serverName string, serverCert *x509.Certificate) error {
	return nil
}
