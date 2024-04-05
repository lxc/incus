package drivers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/units"
)

// ValidateVolume validates the supplied volume config.
func (d *cephobject) ValidateVolume(vol Volume, removeUnknownKeys bool) error {
	return d.validateVolume(vol, nil, removeUnknownKeys)
}

// s3Client returns a configured minio S3 client.
func (d *cephobject) s3Client(creds S3Credentials) (*minio.Client, error) {
	u, err := url.ParseRequestURI(d.config["cephobject.radosgw.endpoint"])
	if err != nil {
		return nil, fmt.Errorf("Failed parsing cephobject.radosgw.endpoint: %w", err)
	}

	var transport http.RoundTripper

	certFilePath := d.config["cephobject.radosgw.endpoint_cert_file"]

	if u.Scheme == "https" && certFilePath != "" {
		// Read in the cert file.
		certs, err := os.ReadFile(certFilePath)
		if err != nil {
			return nil, fmt.Errorf("Failed reading %q: %w", certFilePath, err)
		}

		rootCAs := x509.NewCertPool()

		ok := rootCAs.AppendCertsFromPEM(certs)
		if !ok {
			return nil, fmt.Errorf("Failed adding S3 client certificates")
		}

		// Trust the cert pool in our client.
		config := &tls.Config{
			RootCAs: rootCAs,
		}

		transport = &http.Transport{TLSClientConfig: config}
	}

	minioClient, err := minio.New(path.Join(u.Host, u.Path), &minio.Options{
		Creds:     credentials.NewStaticV4(creds.AccessKey, creds.SecretKey, ""),
		Secure:    u.Scheme == "https",
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	return minioClient, nil
}

// CreateBucket creates a new bucket.
func (d *cephobject) CreateBucket(bucket Volume, op *operations.Operation) error {
	// Check if there is an existing cephobjectRadosgwAdminUser user.
	adminUserInfo, _, err := d.radosgwadminGetUser(context.TODO(), cephobjectRadosgwAdminUser)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed getting admin user %q: %w", cephobjectRadosgwAdminUser, err)
	}

	// Create missing cephobjectRadosgwAdminUser user.
	if adminUserInfo == nil {
		adminUserInfo, err = d.radosgwadminUserAdd(context.TODO(), cephobjectRadosgwAdminUser, 0)
		if err != nil {
			return fmt.Errorf("Failed added admin user %q: %w", cephobjectRadosgwAdminUser, err)
		}
	}

	_, bucketName := project.StorageVolumeParts(bucket.name)
	storageBucketName := d.radosgwBucketName(bucketName)

	// Must be defined before revert so that its not cancelled by time revert.Fail runs.
	ctx, ctxCancel := context.WithTimeout(context.TODO(), time.Duration(time.Second*30))
	defer ctxCancel()

	revert := revert.New()
	defer revert.Fail()

	minioClient, err := d.s3Client(*adminUserInfo)
	if err != nil {
		return err
	}

	bucketExists, err := minioClient.BucketExists(ctx, storageBucketName)
	if err != nil {
		return err
	}

	if bucketExists {
		return api.StatusErrorf(http.StatusConflict, "A bucket for that name already exists")
	}

	// Create new bucket.
	err = minioClient.MakeBucket(ctx, storageBucketName, minio.MakeBucketOptions{})
	if err != nil {
		return fmt.Errorf("Failed creating bucket: %w", err)
	}

	revert.Add(func() { _ = minioClient.RemoveBucket(ctx, storageBucketName) })

	// Create bucket user.
	_, err = d.radosgwadminUserAdd(context.TODO(), storageBucketName, -1)
	if err != nil {
		return fmt.Errorf("Failed creating bucket user: %w", err)
	}

	revert.Add(func() { _ = d.radosgwadminUserDelete(context.TODO(), storageBucketName) })

	// Link bucket to user.
	err = d.radosgwadminBucketLink(context.TODO(), storageBucketName, storageBucketName)
	if err != nil {
		return fmt.Errorf("Failed linking bucket to user: %w", err)
	}

	// Set initial quota if specified.
	if bucket.config["size"] != "" && bucket.config["size"] != "0" {
		err = d.setBucketQuota(bucket, bucket.config["size"])
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// setBucketQuota sets the bucket quota.
func (d *cephobject) setBucketQuota(bucket Volume, quotaSize string) error {
	_, bucketName := project.StorageVolumeParts(bucket.name)
	storageBucketName := d.radosgwBucketName(bucketName)

	sizeBytes, err := units.ParseByteSizeString(quotaSize)
	if err != nil {
		return fmt.Errorf("Failed parsing bucket quota size: %w", err)
	}

	err = d.radosgwadminBucketSetQuota(context.TODO(), storageBucketName, storageBucketName, sizeBytes)
	if err != nil {
		return fmt.Errorf("Failed setting bucket quota: %w", err)
	}

	return nil
}

// DeleteBucket deletes an existing bucket.
func (d *cephobject) DeleteBucket(bucket Volume, op *operations.Operation) error {
	_, bucketName := project.StorageVolumeParts(bucket.name)
	storageBucketName := d.radosgwBucketName(bucketName)

	err := d.radosgwadminBucketDelete(context.TODO(), storageBucketName)
	if err != nil {
		return fmt.Errorf("Failed deleting bucket: %w", err)
	}

	err = d.radosgwadminUserDelete(context.TODO(), storageBucketName)
	if err != nil {
		return fmt.Errorf("Failed deleting bucket user: %w", err)
	}

	return nil
}

// UpdateBucket updates an existing bucket.
func (d *cephobject) UpdateBucket(bucket Volume, changedConfig map[string]string) error {
	newSize, sizeChanged := changedConfig["size"]
	if sizeChanged {
		err := d.setBucketQuota(bucket, newSize)
		if err != nil {
			return err
		}
	}

	return nil
}

// bucketKeyRadosgwAccessRole returns the radosgw access setting for the specified role name.
func (d *cephobject) bucketKeyRadosgwAccessRole(roleName string) (string, error) {
	switch roleName {
	case "read-only":
		return "read", nil
	case "admin":
		return "full", nil
	}

	return "", api.StatusErrorf(http.StatusBadRequest, "Invalid bucket key role")
}

// CreateBucket creates a new bucket.
func (d *cephobject) CreateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error) {
	_, bucketName := project.StorageVolumeParts(bucket.name)
	storageBucketName := d.radosgwBucketName(bucketName)

	accessRole, err := d.bucketKeyRadosgwAccessRole(roleName)
	if err != nil {
		return nil, err
	}

	_, bucketSubUsers, err := d.radosgwadminGetUser(context.TODO(), storageBucketName)
	if err != nil {
		return nil, fmt.Errorf("Failed getting bucket user: %w", err)
	}

	_, subUserExists := bucketSubUsers[keyName]
	if subUserExists {
		return nil, api.StatusErrorf(http.StatusConflict, "A bucket key for that name already exists")
	}

	// Create a sub user for the key on the bucket user.
	newCreds, err := d.radosgwadminSubUserAdd(context.TODO(), storageBucketName, keyName, accessRole, creds.AccessKey, creds.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("Failed creating bucket user: %w", err)
	}

	return newCreds, nil
}

// UpdateBucketKey updates bucket key.
func (d *cephobject) UpdateBucketKey(bucket Volume, keyName string, creds S3Credentials, roleName string, op *operations.Operation) (*S3Credentials, error) {
	_, bucketName := project.StorageVolumeParts(bucket.name)
	storageBucketName := d.radosgwBucketName(bucketName)

	accessRole, err := d.bucketKeyRadosgwAccessRole(roleName)
	if err != nil {
		return nil, err
	}

	_, bucketSubUsers, err := d.radosgwadminGetUser(context.TODO(), storageBucketName)
	if err != nil {
		return nil, fmt.Errorf("Failed getting bucket user: %w", err)
	}

	_, subUserExists := bucketSubUsers[keyName]
	if !subUserExists {
		return nil, api.StatusErrorf(http.StatusConflict, "A bucket key for that name does not exist")
	}

	// We delete and recreate the subuser otherwise if the creds.AccessKey has changed a new access key/secret
	// will be created, leaving the old one behind still active.
	err = d.radosgwadminSubUserDelete(context.TODO(), storageBucketName, keyName)
	if err != nil {
		return nil, fmt.Errorf("Failed deleting bucket key: %w", err)
	}

	// Create a sub user for the key on the bucket user.
	newCreds, err := d.radosgwadminSubUserAdd(context.TODO(), storageBucketName, keyName, accessRole, creds.AccessKey, creds.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("Failed creating bucket user: %w", err)
	}

	return newCreds, err
}

// DeleteBucketKey deletes an existing bucket key.
func (d *cephobject) DeleteBucketKey(bucket Volume, keyName string, op *operations.Operation) error {
	_, bucketName := project.StorageVolumeParts(bucket.name)
	storageBucketName := d.radosgwBucketName(bucketName)

	err := d.radosgwadminSubUserDelete(context.TODO(), storageBucketName, keyName)
	if err != nil {
		return fmt.Errorf("Failed deleting bucket key: %w", err)
	}

	return nil
}

// GetBucketURL returns the URL of the specified bucket.
func (d *cephobject) GetBucketURL(bucketName string) *url.URL {
	u, err := url.ParseRequestURI(d.config["cephobject.radosgw.endpoint"])
	if err != nil {
		return nil
	}

	u.Path = path.Join(u.Path, url.PathEscape(d.radosgwBucketName(bucketName)))

	return u
}
