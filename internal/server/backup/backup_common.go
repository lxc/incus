package backup

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

// WorkingDirPrefix is used when temporary working directories are needed.
const WorkingDirPrefix = "incus_backup"

// CommonBackup represents a common backup.
type CommonBackup struct {
	state                *state.State
	id                   int
	name                 string
	creationDate         time.Time
	expiryDate           time.Time
	optimizedStorage     bool
	compressionAlgorithm string
}

// Name returns the name of the backup.
func (b *CommonBackup) Name() string {
	return b.name
}

// CompressionAlgorithm returns the compression used for the tarball.
func (b *CommonBackup) CompressionAlgorithm() string {
	return b.compressionAlgorithm
}

// SetCompressionAlgorithm sets the tarball compression.
func (b *CommonBackup) SetCompressionAlgorithm(compression string) {
	b.compressionAlgorithm = compression
}

// OptimizedStorage returns whether the backup is to be performed using
// optimization supported by the storage driver.
func (b *CommonBackup) OptimizedStorage() bool {
	return b.optimizedStorage
}

// upload handles backup uploads.
func (b *CommonBackup) upload(filePath string, req *api.BackupTarget) error {
	if req.Protocol != "s3" {
		return fmt.Errorf("Unsupported backup target protocol %q", req.Protocol)
	}

	// Set up an S3 client.
	uri, err := url.Parse(req.URL)
	if err != nil {
		return err
	}

	creds := credentials.NewStaticV4(req.AccessKey, req.SecretKey, "")

	ts := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
	}

	client, err := minio.New(uri.Host, &minio.Options{
		BucketLookup: minio.BucketLookupPath,
		Creds:        creds,
		Secure:       uri.Scheme == "https",
		Transport:    ts,
	})
	if err != nil {
		return err
	}

	// Upload the object.
	tr, err := os.Open(filePath)
	if err != nil {
		return err
	}

	defer tr.Close()

	_, err = client.PutObject(context.Background(), req.BucketName, req.Path, tr, -1, minio.PutObjectOptions{})
	if err != nil {
		return err
	}

	return nil
}
