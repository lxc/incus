package backup

import (
	"archive/tar"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lxc/incus/v6/internal/server/sys"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
)

// TarReader rewinds backup file handle r and returns new tar reader and process cleanup function.
func TarReader(r io.ReadSeeker, sysOS *sys.OS, outputPath string) (*tar.Reader, context.CancelFunc, error) {
	_, err := r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, nil, err
	}

	_, _, unpacker, err := archive.DetectCompressionFile(r)
	if err != nil {
		return nil, nil, err
	}

	if unpacker == nil {
		return nil, nil, errors.New("Unsupported backup compression")
	}

	tr, cancelFunc, err := archive.CompressedTarReader(context.Background(), r, unpacker, outputPath)
	if err != nil {
		return nil, nil, err
	}

	return tr, cancelFunc, nil
}

// Upload handles backup uploads.
func Upload(reader *io.PipeReader, req *api.BackupTarget) error {
	// We want to close the reader as soon as something bad occurs, ensuring that we don't hang on a
	// pipe that's unable to consume anything.
	defer func() { _ = reader.Close() }()

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

	_, err = client.PutObject(context.Background(), req.BucketName, req.Path, reader, -1, minio.PutObjectOptions{})
	if err != nil {
		return err
	}

	return nil
}
