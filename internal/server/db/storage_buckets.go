//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	dqliteDriver "github.com/cowsql/go-cowsql/driver"

	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// StorageBucketFilter used for filtering storage buckets with GetStorageBuckets().
type StorageBucketFilter struct {
	PoolID   *int64
	PoolName *string
	Project  *string
	Name     *string
}

// StorageBucket represents a database storage bucket record.
type StorageBucket struct {
	api.StorageBucket

	ID       int64
	PoolID   int64
	PoolName string
}

// GetStoragePoolBuckets returns all storage buckets.
// If there are no buckets, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned. If memberSpecific is true, then the search is
// restricted to buckets that belong to this member or belong to all members.
func (c *ClusterTx) GetStoragePoolBuckets(ctx context.Context, memberSpecific bool, filters ...StorageBucketFilter) ([]*StorageBucket, error) {
	var q *strings.Builder = &strings.Builder{}
	var args []any

	q.WriteString(`
	SELECT
		projects.name as project,
		storage_pools.name,
		storage_buckets.id,
		storage_buckets.storage_pool_id,
		storage_buckets.name,
		storage_buckets.description,
		IFNULL(nodes.name, "") as location
	FROM storage_buckets
	JOIN projects ON projects.id = storage_buckets.project_id
	JOIN storage_pools ON storage_pools.id = storage_buckets.storage_pool_id
	LEFT JOIN nodes ON nodes.id = storage_buckets.node_id
	`)

	if memberSpecific {
		if len(args) == 0 {
			q.WriteString("WHERE ")
		} else {
			q.WriteString("AND ")
		}

		q.WriteString("(storage_buckets.node_id = ? OR storage_buckets.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	if len(filters) > 0 {
		if len(args) == 0 {
			q.WriteString("WHERE (")
		} else {
			q.WriteString("AND (")
		}

		for i, filter := range filters {
			// Validate filter.
			if !memberSpecific && filter.Name != nil && ((filter.PoolID == nil && filter.PoolName == nil) || filter.Project == nil) {
				return nil, fmt.Errorf("Cannot filter by bucket name without specifying pool and project when doing member inspecific search")
			}

			var qFilters []string

			if filter.PoolID != nil {
				qFilters = append(qFilters, "storage_buckets.storage_pool_id= ?")
				args = append(args, *filter.PoolID)
			}

			if filter.PoolName != nil {
				qFilters = append(qFilters, "storage_pools.name= ?")
				args = append(args, *filter.PoolID)
			}

			if filter.Project != nil {
				qFilters = append(qFilters, "projects.name = ?")
				args = append(args, *filter.Project)
			}

			if filter.Name != nil {
				qFilters = append(qFilters, "storage_buckets.name = ?")
				args = append(args, *filter.Name)
			}

			if qFilters == nil {
				return nil, fmt.Errorf("Invalid storage bucket filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString(fmt.Sprintf("(%s)", strings.Join(qFilters, " AND ")))
		}

		q.WriteString(")")
	}

	var err error
	var buckets []*StorageBucket

	err = query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var bucket StorageBucket

		err := scan(&bucket.Project, &bucket.PoolName, &bucket.ID, &bucket.PoolID, &bucket.Name, &bucket.Description, &bucket.Location)
		if err != nil {
			return err
		}

		buckets = append(buckets, &bucket)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Populate config.
	for i := range buckets {
		err = storagePoolBucketConfig(ctx, c, buckets[i].ID, &buckets[i].StorageBucket)
		if err != nil {
			return nil, err
		}
	}

	return buckets, nil
}

// storagePoolBucketConfig populates the config map of the Storage Bucket with the given ID.
func storagePoolBucketConfig(ctx context.Context, tx *ClusterTx, bucketID int64, bucket *api.StorageBucket) error {
	q := `
	SELECT
		key,
		value
	FROM storage_buckets_config
	WHERE storage_bucket_id=?
	`

	bucket.Config = make(map[string]string)
	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := bucket.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for storage bucket ID %d", key, bucketID)
		}

		bucket.Config[key] = value

		return nil
	}, bucketID)
}

// GetStoragePoolBucket returns the Storage Bucket for the given Storage Pool ID, Project Name and Bucket Name.
// If memberSpecific is true, then the search is restricted to buckets that belong to this member or belong
// to all members.
func (c *ClusterTx) GetStoragePoolBucket(ctx context.Context, poolID int64, projectName string, memberSpecific bool, bucketName string) (*StorageBucket, error) {
	filters := []StorageBucketFilter{{
		PoolID:  &poolID,
		Project: &projectName,
		Name:    &bucketName,
	}}

	buckets, err := c.GetStoragePoolBuckets(ctx, memberSpecific, filters...)
	bucketsLen := len(buckets)
	if (err == nil && bucketsLen <= 0) || errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "Storage bucket not found")
	} else if err == nil && bucketsLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "Storage bucket found on more than one cluster member. Please target a specific member")
	} else if err != nil {
		return nil, err
	}

	return buckets[0], nil
}

// GetStoragePoolLocalBucket returns the local Storage Bucket for the given bucket name.
// The search is restricted to buckets that belong to this member.
func (c *ClusterTx) GetStoragePoolLocalBucket(ctx context.Context, bucketName string) (*StorageBucket, error) {
	filters := []StorageBucketFilter{{
		Name: &bucketName,
	}}

	buckets, err := c.GetStoragePoolBuckets(ctx, true, filters...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	for _, bucket := range buckets {
		if bucket.Location == "" {
			continue // Ignore buckets on remote storage pools.
		}

		return bucket, nil
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Storage bucket not found")
}

// GetStoragePoolLocalBucketByAccessKey returns the local Storage Bucket for the given bucket access key.
// The search is restricted to buckets that belong to this member.
func (c *ClusterTx) GetStoragePoolLocalBucketByAccessKey(ctx context.Context, accessKey string) (*StorageBucket, error) {
	var q *strings.Builder = &strings.Builder{}

	q.WriteString(`
	SELECT
		projects.name as project,
		storage_pools.name,
		storage_buckets.id,
		storage_buckets.storage_pool_id,
		storage_buckets.name,
		storage_buckets.description,
		IFNULL(nodes.name, "") as location
	FROM storage_buckets
	JOIN projects ON projects.id = storage_buckets.project_id
	JOIN storage_pools ON storage_pools.id = storage_buckets.storage_pool_id
	JOIN storage_buckets_keys ON storage_buckets_keys.storage_bucket_id = storage_buckets.id
	JOIN nodes ON nodes.id = storage_buckets.node_id
	WHERE storage_buckets.node_id = ?
	AND storage_buckets_keys.access_key = ?
	`)

	var err error
	var buckets []*StorageBucket
	args := []any{c.nodeID, accessKey}

	err = query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var bucket StorageBucket

		err := scan(&bucket.Project, &bucket.PoolName, &bucket.ID, &bucket.PoolID, &bucket.Name, &bucket.Description, &bucket.Location)
		if err != nil {
			return err
		}

		buckets = append(buckets, &bucket)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	bucketsLen := len(buckets)
	if bucketsLen == 1 {
		// Populate config.
		err = storagePoolBucketConfig(ctx, c, buckets[0].ID, &buckets[0].StorageBucket)
		if err != nil {
			return nil, err
		}

		return buckets[0], nil
	} else if bucketsLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "Multiple storage buckets found for access key")
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Storage bucket access key not found")
}

// CreateStoragePoolBucket creates a new Storage Bucket.
// If memberSpecific is true, then the storage bucket is associated to the current member, rather than being
// associated to all members.
func (c *ClusterTx) CreateStoragePoolBucket(ctx context.Context, poolID int64, projectName string, memberSpecific bool, info api.StorageBucketsPost) (int64, error) {
	var err error
	var bucketID int64
	var nodeID any

	if memberSpecific {
		nodeID = c.nodeID
	}

	// Insert a new Storage Bucket record.
	result, err := c.tx.ExecContext(ctx, `
		INSERT INTO storage_buckets
		(storage_pool_id, node_id, name, description, project_id)
		VALUES (?, ?, ?, ?, (SELECT id FROM projects WHERE name = ?))
		`, poolID, nodeID, info.Name, info.Description, projectName)
	if err != nil {
		var dqliteErr dqliteDriver.Error
		// Detect SQLITE_CONSTRAINT_UNIQUE (2067) errors.
		if errors.As(err, &dqliteErr) && dqliteErr.Code == 2067 {
			return -1, api.StatusErrorf(http.StatusConflict, "A bucket for that name already exists")
		}

		return -1, err
	}

	bucketID, err = result.LastInsertId()
	if err != nil {
		return -1, err
	}

	// Save config.
	err = storageBucketPoolConfigAdd(c.tx, bucketID, info.Config)
	if err != nil {
		return -1, err
	}

	return bucketID, err
}

// storageBucketPoolConfigAdd inserts Storage Bucket config keys.
func storageBucketPoolConfigAdd(tx *sql.Tx, bucketID int64, config map[string]string) error {
	stmt, err := tx.Prepare(`
	INSERT INTO storage_buckets_config
	(storage_bucket_id, key, value)
	VALUES(?, ?, ?)
	`)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(bucketID, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateStoragePoolBucket updates an existing Storage Bucket.
func (c *ClusterTx) UpdateStoragePoolBucket(ctx context.Context, poolID int64, bucketID int64, info *api.StorageBucketPut) error {
	// Update existing Storage Bucket record.
	res, err := c.tx.ExecContext(ctx, `
		UPDATE storage_buckets
		SET description = ?
		WHERE storage_pool_id = ? and id = ?
		`, info.Description, poolID, bucketID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Storage bucket not found")
	}

	// Save config.
	_, err = c.tx.ExecContext(ctx, "DELETE FROM storage_buckets_config WHERE storage_bucket_id=?", bucketID)
	if err != nil {
		return err
	}

	err = storageBucketPoolConfigAdd(c.tx, bucketID, info.Config)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePoolBucket deletes an existing Storage Bucket.
func (c *ClusterTx) DeleteStoragePoolBucket(ctx context.Context, poolID int64, bucketID int64) error {
	// Delete existing Storage Bucket record.
	res, err := c.tx.ExecContext(ctx, `
			DELETE FROM storage_buckets
			WHERE storage_pool_id = ? and id = ?
		`, poolID, bucketID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Storage bucket not found")
	}

	return nil
}

// StorageBucketKeyFilter used for filtering storage bucket keys with GetStoragePoolBucketKeys().
type StorageBucketKeyFilter struct {
	Name *string
}

// StorageBucketKey represents a database storage bucket key record.
type StorageBucketKey struct {
	api.StorageBucketKey

	ID int64
}

// GetStoragePoolBucketKeys returns all storage buckets keys attached to a given storage bucket.
// If there are no bucket keys, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned.
func (c *ClusterTx) GetStoragePoolBucketKeys(ctx context.Context, bucketID int64, filters ...StorageBucketKeyFilter) ([]*StorageBucketKey, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []any{bucketID}

	q.WriteString(`
	SELECT
		storage_buckets_keys.id,
		storage_buckets_keys.name,
		storage_buckets_keys.description,
		storage_buckets_keys.role,
		storage_buckets_keys.access_key,
		storage_buckets_keys.secret_key
	FROM storage_buckets_keys
	WHERE storage_buckets_keys.storage_bucket_id = ?
	`)

	if len(filters) > 0 {
		q.WriteString("AND (")

		for i, filter := range filters {
			var qFilters []string

			if filter.Name != nil {
				qFilters = append(qFilters, "storage_buckets_keys.name = ?")
				args = append(args, *filter.Name)
			}

			if qFilters == nil {
				return nil, fmt.Errorf("Invalid storage bucket key filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString(fmt.Sprintf("(%s)", strings.Join(qFilters, " AND ")))
		}

		q.WriteString(")")
	}

	var err error
	var bucketKeys []*StorageBucketKey

	err = query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var bucketKey StorageBucketKey

		err := scan(&bucketKey.ID, &bucketKey.Name, &bucketKey.Description, &bucketKey.Role, &bucketKey.AccessKey, &bucketKey.SecretKey)
		if err != nil {
			return err
		}

		bucketKeys = append(bucketKeys, &bucketKey)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return bucketKeys, nil
}

// GetStoragePoolBucketKey returns the Storage Bucket Key for the given Bucket ID and Key Name.
func (c *ClusterTx) GetStoragePoolBucketKey(ctx context.Context, bucketID int64, keyName string) (*StorageBucketKey, error) {
	filters := []StorageBucketKeyFilter{{
		Name: &keyName,
	}}

	bucketKeys, err := c.GetStoragePoolBucketKeys(ctx, bucketID, filters...)
	bucketKeysLen := len(bucketKeys)
	if (err == nil && bucketKeysLen <= 0) || errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "Storage bucket key not found")
	} else if err == nil && bucketKeysLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "More than one storage bucket key found")
	} else if err != nil {
		return nil, err
	}

	return bucketKeys[0], nil
}

// CreateStoragePoolBucketKey creates a new Storage Bucket Key.
func (c *ClusterTx) CreateStoragePoolBucketKey(ctx context.Context, bucketID int64, info api.StorageBucketKeysPost) (int64, error) {
	var err error
	var bucketKeyID int64

	// Check there isn't another bucket with the same access key on the local server.
	bucket, err := c.GetStoragePoolLocalBucketByAccessKey(ctx, info.AccessKey)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return -1, err
	} else if bucket != nil {
		return -1, api.StatusErrorf(http.StatusConflict, "A bucket key using that access key already exists on this server")
	}

	// Insert a new Storage Bucket Key record.
	result, err := c.tx.ExecContext(ctx, `
		INSERT INTO storage_buckets_keys
		(storage_bucket_id, name, description, role, access_key, secret_key)
		VALUES (?, ?, ?, ?, ?, ?)
		`, bucketID, info.Name, info.Description, info.Role, info.AccessKey, info.SecretKey)
	if err != nil {
		var dqliteErr dqliteDriver.Error
		// Detect SQLITE_CONSTRAINT_UNIQUE (2067) errors.
		if errors.As(err, &dqliteErr) && dqliteErr.Code == 2067 {
			return -1, api.StatusErrorf(http.StatusConflict, "A bucket key for that name already exists")
		}

		return -1, err
	}

	bucketKeyID, err = result.LastInsertId()
	if err != nil {
		return -1, err
	}

	return bucketKeyID, err
}

// UpdateStoragePoolBucketKey updates an existing Storage Bucket Key.
func (c *ClusterTx) UpdateStoragePoolBucketKey(ctx context.Context, bucketID int64, bucketKeyID int64, info *api.StorageBucketKeyPut) error {
	// Check there isn't another bucket with the same access key on the local server.
	bucket, err := c.GetStoragePoolLocalBucketByAccessKey(ctx, info.AccessKey)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	} else if bucket != nil && bucket.ID != bucketID {
		return api.StatusErrorf(http.StatusConflict, "A bucket key using that access key already exists on this server")
	}

	// Update existing Storage Bucket Key record.
	res, err := c.tx.ExecContext(ctx, `
		UPDATE storage_buckets_keys
		SET description = ?, role = ?, access_key = ?, secret_key = ?
		WHERE storage_bucket_id = ? and id = ?
		`, info.Description, info.Role, info.AccessKey, info.SecretKey, bucketID, bucketKeyID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Storage bucket key not found")
	}

	return nil
}

// DeleteStoragePoolBucketKey deletes an existing Storage Bucket Key.
func (c *ClusterTx) DeleteStoragePoolBucketKey(ctx context.Context, bucketID int64, keyID int64) error {
	// Delete existing Storage Bucket record.
	res, err := c.tx.ExecContext(ctx, `
			DELETE FROM storage_buckets_keys
			WHERE storage_bucket_id = ? and id = ?
		`, bucketID, keyID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Storage bucket key not found")
	}

	return nil
}

// GetStoragePoolBucketWithID returns the volume with the given ID.
func (c *ClusterTx) GetStoragePoolBucketWithID(ctx context.Context, bucketID int) (StorageBucket, error) {
	var response StorageBucket

	stmt := `
SELECT
	projects.name as project,
	storage_pools.name,
	storage_buckets.id,
	storage_buckets.storage_pool_id,
	storage_buckets.name,
	storage_buckets.description,
	IFNULL(nodes.name, "") as location
FROM storage_buckets
JOIN projects ON projects.id = storage_buckets.project_id
JOIN storage_pools ON storage_pools.id = storage_buckets.storage_pool_id
LEFT JOIN nodes ON nodes.id = storage_buckets.node_id
WHERE storage_buckets.id = ?
`

	err := c.tx.QueryRowContext(ctx, stmt, bucketID).Scan(&response.Project, &response.PoolName, &response.ID, &response.PoolID, &response.Name, &response.Description, &response.Location)
	if err != nil {
		if err == sql.ErrNoRows {
			return StorageBucket{}, api.StatusErrorf(http.StatusNotFound, "Storage pool bucket not found")
		}

		return StorageBucket{}, err
	}

	response.Config, err = c.storageVolumeConfigGet(ctx, response.ID, false)
	if err != nil {
		return StorageBucket{}, err
	}

	return response, nil
}

// GetStorageBucketURIs returns the URIs of the storage buckets, specifying
// target node if applicable.
func (c *ClusterTx) GetStorageBucketURIs(ctx context.Context, project string) ([]string, error) {
	filter := StorageBucketFilter{Project: &project}
	bucketInfo, err := c.GetStoragePoolBuckets(ctx, false, filter)
	if err != nil {
		return nil, err
	}

	uris := []string{}
	for _, info := range bucketInfo {
		uri := api.NewURL().Path(version.APIVersion, "storage-pools", info.PoolName, "buckets", info.Name).Project(project)

		if info.Location != "" {
			uri.Target(info.Location)
		}

		uris = append(uris, uri.String())
	}

	return uris, nil
}
