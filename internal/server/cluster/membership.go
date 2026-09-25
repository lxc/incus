package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

type ClusterResources struct {
	Pools      map[string]map[string]string
	Networks   map[string]map[string]string
	Operations []cluster.Operation
}

// Bootstrap turns a non-clustered server into the first (and leader)
// member of a new cluster.
//
// This instance must already have its cluster.https_address set and be listening
// on the associated network address.

// EnsureServerCertificateTrusted adds the serverCert to the DB trusted certificates store using the serverName.
// If a certificate with the same fingerprint is already in the trust store, but is of the wrong type or name then
// the existing certificate is updated to the correct type and name. If the existing certificate is the correct
// type but the wrong name then an error is returned. And if the existing certificate is the correct type and name
// then nothing more is done.
func EnsureServerCertificateTrusted(serverName string, serverCertx509 *x509.Certificate, tx *db.ClusterTx) error {
	fingerprint := localtls.CertFingerprint(serverCertx509)

	dbCert := cluster.Certificate{
		Fingerprint: fingerprint,
		Type:        certificate.TypeServer, // Server type for intra-member communication.
		Name:        serverName,
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertx509.Raw})),
	}

	// Add our server cert to the DB trust store (so when other members join this cluster they will be
	// able to trust intra-cluster requests from this member).
	ctx := context.Background()
	existingCert, _ := cluster.GetCertificate(ctx, tx.Tx(), dbCert.Fingerprint)
	if existingCert != nil {
		if existingCert.Name != dbCert.Name && existingCert.Type == certificate.TypeServer {
			// Don't alter an existing server certificate that has our fingerprint but not our name.
			// Something is wrong as this shouldn't happen.
			return fmt.Errorf("Existing server certificate with different name %q already in trust store", existingCert.Name)
		} else if existingCert.Name != dbCert.Name && existingCert.Type != certificate.TypeServer {
			// Ensure that if a client certificate already exists that matches our fingerprint, that it
			// has the correct name and type for cluster operation, to allow us to associate member
			// server names to certificate names.
			err := cluster.UpdateCertificate(ctx, tx.Tx(), dbCert.Fingerprint, dbCert)
			if err != nil {
				return fmt.Errorf("Failed updating certificate name and type in trust store: %w", err)
			}
		}
	} else {
		_, err := cluster.CreateCertificate(ctx, tx.Tx(), dbCert)
		if err != nil {
			return fmt.Errorf("Failed adding server certificate to trust store: %w", err)
		}
	}

	return nil
}

// SchemaVersion holds the version of the cluster database schema.
var SchemaVersion = cluster.SchemaVersion
