//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/lxc/incus/internal/server/certificate"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/query"
)

// UpdateCertificate updates a certificate in the db.
func (db *DB) UpdateCertificate(ctx context.Context, fingerprint string, cert cluster.Certificate, projectNames []string) error {
	err := db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		id, err := cluster.GetCertificateID(ctx, tx.Tx(), fingerprint)
		if err != nil {
			return err
		}

		err = cluster.UpdateCertificate(ctx, tx.Tx(), fingerprint, cert)
		if err != nil {
			return err
		}

		return cluster.UpdateCertificateProjects(ctx, tx.Tx(), int(id), projectNames)
	})

	return err
}

// GetCertificates returns all available local certificates.
func (n *NodeTx) GetCertificates(ctx context.Context) ([]cluster.Certificate, error) {
	type cert struct {
		fingerprint string
		certType    certificate.Type
		name        string
		certificate string
	}

	sql := "SELECT fingerprint, type, name, certificate FROM certificates"
	dbCerts := []cert{}
	err := query.Scan(ctx, n.tx, sql, func(scan func(dest ...any) error) error {
		dbCert := cert{}

		err := scan(&dbCert.fingerprint, &dbCert.certType, &dbCert.name, &dbCert.certificate)
		if err != nil {
			return err
		}

		dbCerts = append(dbCerts, dbCert)

		return nil
	})
	if err != nil {
		return nil, err
	}

	certs := make([]cluster.Certificate, 0, len(dbCerts))
	for _, dbCert := range dbCerts {
		certs = append(certs, cluster.Certificate{
			Fingerprint: dbCert.fingerprint,
			Type:        dbCert.certType,
			Name:        dbCert.name,
			Certificate: dbCert.certificate,
		})
	}

	return certs, nil
}

// ReplaceCertificates removes all existing certificates from the local certificates table and replaces them with
// the ones provided.
func (n *NodeTx) ReplaceCertificates(certs []cluster.Certificate) error {
	_, err := n.tx.Exec("DELETE FROM certificates")
	if err != nil {
		return err
	}

	sql := "INSERT INTO certificates (fingerprint, type, name, certificate) VALUES(?,?,?,?)"
	for _, cert := range certs {
		_, err = n.tx.Exec(sql, cert.Fingerprint, cert.Type, cert.Name, cert.Certificate)
		if err != nil {
			return err
		}
	}

	return nil
}
