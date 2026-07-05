package s3

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/lxc/incus/v7/internal/instancewriter"
	"github.com/lxc/incus/v7/internal/server/backup"
	"github.com/lxc/incus/v7/internal/server/storage/s3util"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/validate"
)

// TransferManager represents a transfer manager.
type TransferManager struct {
	s3URL     *url.URL
	accessKey string
	secretKey string

	// serverCert is the certificate expected from the S3 endpoint.
	// When set, it's pinned during the TLS handshake, otherwise verification is skipped.
	serverCert *x509.Certificate
}

// NewTransferManager instantiates a new TransferManager struct.
// When serverCert is set, the endpoint's certificate is pinned against it.
func NewTransferManager(s3URL *url.URL, accessKey string, secretKey string, serverCert *x509.Certificate) TransferManager {
	return TransferManager{
		s3URL:      s3URL,
		accessKey:  accessKey,
		secretKey:  secretKey,
		serverCert: serverCert,
	}
}

// DownloadAllFiles downloads all files from a bucket and writes them to a tar writer.
func (t TransferManager) DownloadAllFiles(bucketName string, tarWriter *instancewriter.InstanceTarWriter) error {
	logger.Debugf("Downloading all files from bucket %s", bucketName)
	logger.Debugf("Endpoint: %s", t.getEndpoint())

	s3Client, err := t.getS3Client()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	paginator := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			logger.Errorf("Failed to list objects: %v", err)
			return err
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)

			// Skip directories because they are part of the key of an actual file
			if strings.HasSuffix(key, "/") {
				continue
			}

			out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(key),
			})
			if err != nil {
				logger.Errorf("Failed to get object: %v", err)
				return err
			}

			fi := instancewriter.FileInfo{
				FileName:    fmt.Sprintf("backup/bucket/%s", key),
				FileSize:    aws.ToInt64(obj.Size),
				FileMode:    0o600,
				FileModTime: time.Now(),
			}

			logger.Debugf("Writing file %s to tar writer", key)
			logger.Debugf("File size: %d", fi.FileSize)

			err = tarWriter.WriteFileFromReader(out.Body, &fi)
			if err != nil {
				logger.Errorf("Failed to write file to tar writer: %v", err)
				_ = out.Body.Close()
				return err
			}

			err = out.Body.Close()
			if err != nil {
				logger.Errorf("Failed to close object: %v", err)
				return err
			}
		}
	}

	return nil
}

// UploadAllFiles uploads all the provided files to the bucket.
func (t TransferManager) UploadAllFiles(bucketName string, srcData io.ReadSeeker) error {
	logger.Debugf("Uploading all files to bucket %s", bucketName)
	logger.Debugf("Endpoint: %s", t.getEndpoint())

	s3Client, err := t.getS3Client()
	if err != nil {
		return err
	}

	uploader := transfermanager.New(s3Client)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	// Create temp path and remove it after wards
	mountPath, err := os.MkdirTemp("", "incus_bucket_import_*")
	if err != nil {
		return err
	}

	defer logger.WarnOnError(func() error { return os.RemoveAll(mountPath) }, "Failed to remove temporary mount path")
	logger.Debugf("Created temp mount path %s", mountPath)

	tr, cancelFunc, err := backup.TarReader(srcData, nil, mountPath)
	if err != nil {
		return err
	}

	defer cancelFunc()

	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// End of archive.
				break
			}

			return err
		}

		// Skip anything that's not in the bucket itself.
		if !strings.HasPrefix(hdr.Name, "backup/bucket/") {
			continue
		}

		fileName := strings.TrimPrefix(hdr.Name, "backup/bucket/")

		_, err = uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(fileName),
			Body:   tr,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (t TransferManager) getS3Client() (*s3.Client, error) {
	httpClient := &http.Client{}
	if t.isSecureEndpoint() {
		httpClient.Transport = getTransport(t.serverCert)
	}

	cfg := aws.Config{
		Region:      s3util.RegionFromURL(t.s3URL),
		Credentials: credentials.NewStaticCredentialsProvider(t.accessKey, t.secretKey, ""),
		HTTPClient:  httpClient,
	}

	scheme := "http"
	if t.isSecureEndpoint() {
		scheme = "https"
	}

	endpoint := fmt.Sprintf("%s://%s", scheme, t.getEndpoint())

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	}), nil
}

func (t TransferManager) getEndpoint() string {
	hostname := t.s3URL.Hostname()
	if validate.IsNetworkAddressV6(hostname) == nil {
		hostname = fmt.Sprintf("[%s]", hostname)
	}

	return fmt.Sprintf("%s:%s", hostname, t.s3URL.Port())
}

func (t TransferManager) isSecureEndpoint() bool {
	return t.s3URL.Scheme == "https"
}

func getTransport(serverCert *x509.Certificate) *http.Transport {
	// Get a basic TLS configuration.
	tlsConfig := localtls.InitTLSConfig()

	// The endpoint uses a self-signed certificate with no usable server name, so the
	// default chain validation is disabled and the certificate is pinned instead when known.
	tlsConfig.InsecureSkipVerify = true

	if serverCert != nil {
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) < 1 {
				return errors.New("Missing server certificate")
			}

			if !bytes.Equal(rawCerts[0], serverCert.Raw) {
				return errors.New("Server certificate doesn't match expected certificate")
			}

			return nil
		}
	}

	return &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig:    tlsConfig,
	}
}
