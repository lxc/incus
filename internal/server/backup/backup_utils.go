package backup

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/lxc/incus/v7/internal/server/storage/s3util"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/archive"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
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
	defer logger.WarnOnError(reader.Close, "Failed to close reader")

	if req.Protocol != "s3" {
		return fmt.Errorf("Unsupported backup target protocol %q", req.Protocol)
	}

	// Set up an S3 client.
	uri, err := url.Parse(req.URL)
	if err != nil {
		return err
	}

	// Get a basic TLS client.
	tlsConfig := localtls.InitTLSConfig()

	// Setup the transport.
	ts := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig:    tlsConfig,
	}

	cfg := aws.Config{
		Region:      s3util.RegionFromURL(uri),
		Credentials: credentials.NewStaticCredentialsProvider(req.AccessKey, req.SecretKey, ""),
		HTTPClient:  &http.Client{Transport: ts},
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("%s://%s", uri.Scheme, uri.Host))
		o.UsePathStyle = true
	})

	uploader := transfermanager.New(client)

	_, err = uploader.UploadObject(context.Background(), &transfermanager.UploadObjectInput{
		Bucket: aws.String(req.BucketName),
		Key:    aws.String(req.Path),
		Body:   reader,
	})
	if err != nil {
		return err
	}

	return nil
}
