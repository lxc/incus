package backup

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/lxc/incus/v7/internal/server/storage/s3util"
	"github.com/lxc/incus/v7/internal/server/sys"
	localUtil "github.com/lxc/incus/v7/internal/server/util"
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

// uploadResponseLimit bounds what is read back from an upload target.
const uploadResponseLimit = 1024 * 1024

// uploadIdleTimeout bounds how long an upload target may stall a transfer.
const uploadIdleTimeout = time.Minute

// uploadTransport caps the size of every response from the upload target.
type uploadTransport struct {
	*http.Transport
}

// RoundTrip performs the request and bounds the response body.
func (t *uploadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	resp.Body = struct {
		io.Reader
		io.Closer
	}{localUtil.MaxBytesReader(resp.Body, uploadResponseLimit), resp.Body}

	return resp, nil
}

// uploadConn requires progress on the connection within the idle timeout.
type uploadConn struct {
	net.Conn
}

// Read reads from the connection with a deadline.
func (c *uploadConn) Read(p []byte) (int, error) {
	err := c.SetReadDeadline(time.Now().Add(uploadIdleTimeout))
	if err != nil {
		return 0, err
	}

	return c.Conn.Read(p)
}

// Write writes to the connection with a deadline per chunk, so large parts need progress rather than a minimum rate.
func (c *uploadConn) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		err := c.SetWriteDeadline(time.Now().Add(uploadIdleTimeout))
		if err != nil {
			return written, err
		}

		n, err := c.Conn.Write(p[written:min(len(p), written+64*1024)])
		written += n
		if err != nil {
			return written, err
		}
	}

	return written, nil
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

	// Setup the transport, the target is chosen by the client.
	ts := &uploadTransport{Transport: &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableCompression:  true,
		TLSClientConfig:     tlsConfig,
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			conn, err := localtls.RFC3493Dialer(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			return &uploadConn{Conn: conn}, nil
		},
	}}

	// Non-AWS stores can't have their region guessed from the URL.
	region := req.Region
	if region == "" {
		region = s3util.RegionFromURL(uri)
	}

	cfg := aws.Config{
		Region:      region,
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
