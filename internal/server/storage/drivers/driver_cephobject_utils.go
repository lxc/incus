package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// radosgwadmin wrapper around radosgw-admin command.
func (d *cephobject) radosgwadmin(ctx context.Context, args ...string) (string, error) {
	_, ok := ctx.Deadline()
	if !ok {
		// Set default timeout of 30s if no deadline context provided.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(30*time.Second))
		defer cancel()
	}

	cmd := []string{"radosgw-admin", "--cluster", d.config["cephobject.cluster_name"], "--id", d.config["cephobject.user.name"]}
	cmd = append(cmd, args...)

	return subprocess.RunCommandContext(ctx, cmd[0], cmd[1:]...)
}

// radosgwadminGetUser returns credentials for an existing radosgw user (and its sub users).
// If no user exists returns api.StatusError with status code set to http.StatusNotFound.
func (d *cephobject) radosgwadminGetUser(ctx context.Context, user string) (*S3Credentials, map[string]S3Credentials, error) {
	out, err := d.radosgwadmin(ctx, "user", "info", "--uid", user)

	if err != nil {
		status, _ := linux.ExitStatus(err)
		if status == 22 {
			return nil, nil, api.StatusErrorf(http.StatusNotFound, "User not found")
		}

		return nil, nil, fmt.Errorf("Failed getting user %q info: %w", user, err)
	}

	resp := struct {
		SubUsers []struct {
			ID string `json:"id"`
		} `json:"subusers"`
		Keys []struct {
			S3Credentials `yaml:",inline"`
			User          string `json:"user"`
		} `json:"keys"`
	}{}

	err = json.Unmarshal([]byte(out), &resp)
	if err != nil {
		return nil, nil, err
	}

	// Get list of sub user names and store them without the main user prefix.
	subUsers := make(map[string]S3Credentials, len(resp.SubUsers))
	for _, subUser := range resp.SubUsers {
		subUserName := strings.TrimPrefix(subUser.ID, fmt.Sprintf("%s:", user))
		subUsers[subUserName] = S3Credentials{}
	}

	var userKey *S3Credentials

	// Iterate through the keys extracting the main user key and keys for the known sub users.
	for _, key := range resp.Keys {
		if key.User == user {
			userKey = &S3Credentials{
				AccessKey: key.AccessKey,
				SecretKey: key.SecretKey,
			}
		} else {
			for subUserName := range subUsers {
				if strings.TrimPrefix(key.User, fmt.Sprintf("%s:", user)) == subUserName {
					subUser := subUsers[subUserName]
					subUser.AccessKey = key.AccessKey
					subUser.SecretKey = key.SecretKey
					subUsers[subUserName] = subUser
				}
			}
		}
	}

	if userKey == nil {
		return nil, nil, fmt.Errorf("S3 credentials not found for %q user", user)
	}

	return userKey, subUsers, nil
}

// radosgwadminUserAdd creates a radosgw user and return generated credentials.
func (d *cephobject) radosgwadminUserAdd(ctx context.Context, user string, maxBuckets int) (*S3Credentials, error) {
	revert := revert.New()
	defer revert.Fail()

	out, err := d.radosgwadmin(ctx, "user", "create", "--max-buckets", fmt.Sprintf("%d", maxBuckets), "--display-name", user, "--uid", user)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = d.radosgwadminUserDelete(ctx, user) })

	creds := struct {
		Keys []struct {
			S3Credentials `yaml:",inline"`
			User          string `json:"user"`
		} `json:"keys"`
	}{}

	err = json.Unmarshal([]byte(out), &creds)
	if err != nil {
		return nil, err
	}

	for _, key := range creds.Keys {
		if key.User == user {
			revert.Success()

			return &S3Credentials{AccessKey: key.AccessKey, SecretKey: key.SecretKey}, err
		}
	}

	return nil, fmt.Errorf("S3 credentials not found for %q user", user)
}

// radosgwadminUserDelete deletes radosgw user.
func (d *cephobject) radosgwadminUserDelete(ctx context.Context, user string) error {
	_, err := d.radosgwadmin(ctx, "user", "rm", "--uid", user, "--purge-data")

	return err
}

// radosgwadminSubUserAdd adds a radosgw sub user.
func (d *cephobject) radosgwadminSubUserAdd(ctx context.Context, user string, subuser string, accessRole string, accessKey string, secretKey string) (*S3Credentials, error) {
	revert := revert.New()
	defer revert.Fail()

	args := []string{"subuser", "create", "--uid", user, "--key-type", "s3", "--subuser", subuser, "--access", accessRole}

	if accessKey == "" {
		args = append(args, "--gen-access-key")
	} else {
		args = append(args, "--access-key", accessKey)
	}

	if secretKey == "" {
		args = append(args, "--gen-secret")
	} else {
		args = append(args, "--secret", secretKey)
	}

	out, err := d.radosgwadmin(ctx, args...)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = d.radosgwadminUserDelete(ctx, user) })

	creds := struct {
		Keys []struct {
			S3Credentials `yaml:",inline"`
			User          string `json:"user"`
		} `json:"keys"`
	}{}

	err = json.Unmarshal([]byte(out), &creds)
	if err != nil {
		return nil, err
	}

	keyUser := fmt.Sprintf("%s:%s", user, subuser)

	for _, key := range creds.Keys {
		if key.User == keyUser {
			revert.Success()

			return &S3Credentials{AccessKey: key.AccessKey, SecretKey: key.SecretKey}, err
		}
	}

	return nil, fmt.Errorf("S3 credentials not found for %q user", keyUser)
}

// radosgwadminSubUserDelete deletes radosgw sub-user.
func (d *cephobject) radosgwadminSubUserDelete(ctx context.Context, user string, subuser string) error {
	_, err := d.radosgwadmin(ctx, "subuser", "rm", "--uid", user, "--subuser", subuser)

	return err
}

// radosgwadminBucketDelete deletes radosgw bucket.
func (d *cephobject) radosgwadminBucketDelete(ctx context.Context, bucket string) error {
	_, err := d.radosgwadmin(ctx, "bucket", "rm", "--bucket", bucket, "--purge-objects")

	return err
}

// radosgwadminBucketLink links a bucket to a user.
func (d *cephobject) radosgwadminBucketLink(ctx context.Context, bucket string, user string) error {
	_, err := d.radosgwadmin(ctx, "bucket", "link", "--bucket", bucket, "--uid", user)

	return err
}

// radosgwadminBucketSetQuota sets bucket quota.
func (d *cephobject) radosgwadminBucketSetQuota(ctx context.Context, bucket string, user string, size int64) error {
	if size > 0 {
		_, err := d.radosgwadmin(ctx, "quota", "enable", "--quota-scope=bucket", "--uid", user)
		if err != nil {
			return err
		}

		_, err = d.radosgwadmin(ctx, "quota", "set", "--quota-scope=bucket", "--uid", user, "--max-size", fmt.Sprintf("%d", size))
		if err != nil {
			return err
		}
	} else {
		_, err := d.radosgwadmin(ctx, "quota", "disable", "--quota-scope=bucket", "--uid", user)
		if err != nil {
			return err
		}

		_, err = d.radosgwadmin(ctx, "quota", "set", "--quota-scope=bucket", "--uid", user, "--max-size", "-1")
		if err != nil {
			return err
		}
	}

	return nil
}

// radosgwadminBucketList returns the list of buckets.
func (d *cephobject) radosgwadminBucketList(ctx context.Context) ([]string, error) {
	out, err := d.radosgwadmin(ctx, "bucket", "list")
	if err != nil {
		return nil, err
	}

	buckets := []string{}

	err = json.Unmarshal([]byte(out), &buckets)
	if err != nil {
		return nil, err
	}

	return buckets, nil
}

// radosgwBucketName returns the bucket name to use for the actual radosgw bucket.
func (d *cephobject) radosgwBucketName(bucketName string) string {
	return fmt.Sprintf("%s%s", d.config["cephobject.bucket.name_prefix"], bucketName)
}
