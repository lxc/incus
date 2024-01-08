package s3

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/incus/internal/instancewriter"
	"github.com/lxc/incus/shared/logger"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type TransferManager struct {
	endpoint  string
	accessKey string
	secretKey string
}

func NewTransferManager(endpoint, accessKey, secretKey string) TransferManager {
	return TransferManager{
		endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

func (t TransferManager) DownloadAllFiles(bucketName string, tarWriter *instancewriter.InstanceTarWriter) error {
	logger.Debugf("Downloading all files from bucket %s", bucketName)
	logger.Debugf("Endpoint: %s", t.endpoint)

	transport, err := t.getTransport()
	if err != nil {
		return err
	}

	minioClient, err := minio.New(t.endpoint, &minio.Options{
		BucketLookup: minio.BucketLookupPath,
		Creds:        credentials.NewStaticV4(t.accessKey, t.secretKey, ""),
		Secure:       true,
		Transport:    transport,
	})
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
		defer object.Close()

		// TODO: Check if this the best way to skip directories
		//
		// Skip directories because they are part of the key of an actual file
		if objectInfo.Key[len(objectInfo.Key)-1] == '/' {
			continue
		}

		fi := instancewriter.FileInfo{
			FileName:    fmt.Sprintf("bucket/%s", objectInfo.Key),
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
	}

	return nil
}

func (t TransferManager) getTransport() (*http.Transport, error) {
	// TODO: Get help from the community to fix this

	// tr, err := minio.DefaultTransport(true)
	// if err != nil {
	// 	return nil, err
	// }

	// certPath := internalUtil.VarPath("server.crt")
	// cert, err := os.ReadFile(certPath)
	// if err != nil {
	// 	return nil, err
	// }

	// TODO: This creates panic: runtime error: invalid memory address or nil pointer dereference

	// ok := tr.TLSClientConfig.RootCAs.AppendCertsFromPEM(cert)
	// if !ok {
	// 	return nil, errors.New("Error creating the minio connection: error parsing the certificate")
	// }

	// return tr, nil

	return &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
	}, nil
}
