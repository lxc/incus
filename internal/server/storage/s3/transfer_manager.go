package s3

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lxc/incus/v6/internal/instancewriter"
	"github.com/lxc/incus/v6/internal/server/backup"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/validate"
)

// TransferManager represents a transfer manager.
type TransferManager struct {
	s3URL     *url.URL
	accessKey string
	secretKey string
}

// NewTransferManager instantiates a new TransferManager struct.
func NewTransferManager(s3URL *url.URL, accessKey string, secretKey string) TransferManager {
	return TransferManager{
		s3URL:     s3URL,
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

// DownloadAllFiles downloads all files from a bucket and writes them to a tar writer.
func (t TransferManager) DownloadAllFiles(bucketName string, tarWriter *instancewriter.InstanceTarWriter) error {
	logger.Debugf("Downloading all files from bucket %s", bucketName)
	logger.Debugf("Endpoint: %s", t.getEndpoint())

	minioClient, err := t.getMinioClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	objectCh := minioClient.ListObjects(ctx, bucketName, minio.ListObjectsOptions{
		Recursive: true,
	})

	for objectInfo := range objectCh {
		if objectInfo.Err != nil {
			logger.Errorf("Failed to get object info: %v", err)
			return objectInfo.Err
		}

		object, err := minioClient.GetObject(ctx, bucketName, objectInfo.Key, minio.GetObjectOptions{})
		if err != nil {
			logger.Errorf("Failed to get object: %v", err)
			return err
		}

		// Skip directories because they are part of the key of an actual file
		if objectInfo.Key[len(objectInfo.Key)-1] == '/' {
			continue
		}

		fi := instancewriter.FileInfo{
			FileName:    fmt.Sprintf("backup/bucket/%s", objectInfo.Key),
			FileSize:    objectInfo.Size,
			FileMode:    0600,
			FileModTime: time.Now(),
		}

		logger.Debugf("Writing file %s to tar writer", objectInfo.Key)
		logger.Debugf("File size: %d", objectInfo.Size)

		err = tarWriter.WriteFileFromReader(object, &fi)
		if err != nil {
			logger.Errorf("Failed to write file to tar writer: %v", err)
			return err
		}

		err = object.Close()
		if err != nil {
			logger.Errorf("Failed to close object: %v", err)
			return err
		}
	}

	return nil
}

// UploadAllFiles uploads all the provided files to the bucket.
func (t TransferManager) UploadAllFiles(bucketName string, srcData io.ReadSeeker) error {
	logger.Debugf("Uploading all files to bucket %s", bucketName)
	logger.Debugf("Endpoint: %s", t.getEndpoint())

	minioClient, err := t.getMinioClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	// Create temp path and remove it after wards
	mountPath, err := os.MkdirTemp("", "incus_bucket_import_*")
	if err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(mountPath) }()
	logger.Debugf("Created temp mount path %s", mountPath)

	tr, cancelFunc, err := backup.TarReader(srcData, nil, mountPath)
	if err != nil {
		return err
	}

	defer cancelFunc()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive.
		}

		// Skip index.yaml file
		if hdr.Name == "backup/index.yaml" {
			continue
		}

		// Skip directories because they are part of the key of an actual file
		fileName := hdr.Name[len("backup/bucket/"):]

		_, err = minioClient.PutObject(ctx, bucketName, fileName, tr, -1, minio.PutObjectOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (t TransferManager) getMinioClient() (*minio.Client, error) {
	bucketLookup := minio.BucketLookupPath
	creds := credentials.NewStaticV4(t.accessKey, t.secretKey, "")

	if t.isSecureEndpoint() {
		return minio.New(t.getEndpoint(), &minio.Options{
			BucketLookup: bucketLookup,
			Creds:        creds,
			Secure:       true,
			Transport:    getTransport(),
		})
	}

	return minio.New(t.getEndpoint(), &minio.Options{
		BucketLookup: bucketLookup,
		Creds:        creds,
		Secure:       false,
	})
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

func getTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
	}
}
