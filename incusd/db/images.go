//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lxc/incus/incusd/db/cluster"
	"github.com/lxc/incus/incusd/db/query"
	"github.com/lxc/incus/incusd/instance/instancetype"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
)

// ImageSourceProtocol maps image source protocol codes to human-readable names.
var ImageSourceProtocol = map[int]string{
	0: "incus",
	1: "direct",
	2: "simplestreams",
}

// GetLocalImagesFingerprints returns the fingerprints of all local images.
func (c *ClusterTx) GetLocalImagesFingerprints(ctx context.Context) ([]string, error) {
	q := `
SELECT images.fingerprint
  FROM images_nodes
  JOIN images ON images.id = images_nodes.image_id
 WHERE node_id = ?
`
	return query.SelectStrings(ctx, c.tx, q, c.nodeID)
}

// GetImageSource returns the image source with the given ID.
func (c *ClusterTx) GetImageSource(ctx context.Context, imageID int) (int, api.ImageSource, error) {
	q := `SELECT id, server, protocol, certificate, alias FROM images_source WHERE image_id=?`
	type imagesSource struct {
		ID          int
		Server      string
		Protocol    int
		Certificate string
		Alias       string
	}

	sources := []imagesSource{}
	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		s := imagesSource{}

		err := scan(&s.ID, &s.Server, &s.Protocol, &s.Certificate, &s.Alias)
		if err != nil {
			return err
		}

		sources = append(sources, s)

		return nil
	}, imageID)
	if err != nil {
		return -1, api.ImageSource{}, err
	}

	if len(sources) == 0 {
		return -1, api.ImageSource{}, api.StatusErrorf(http.StatusNotFound, "Image source not found")
	}

	source := sources[0]

	protocol, found := ImageSourceProtocol[source.Protocol]
	if !found {
		return -1, api.ImageSource{}, fmt.Errorf("Invalid protocol: %d", source.Protocol)
	}

	result := api.ImageSource{
		Server:      source.Server,
		Protocol:    protocol,
		Certificate: source.Certificate,
		Alias:       source.Alias,
	}

	return source.ID, result, nil
}

// Fill extra image fields such as properties and alias. This is called after
// fetching a single row from the images table.
func (c *ClusterTx) imageFill(ctx context.Context, id int, image *api.Image, create, expire, used, upload *time.Time, arch int, imageType int) error {
	// Some of the dates can be nil in the DB, let's process them.
	if create != nil {
		image.CreatedAt = *create
	} else {
		image.CreatedAt = time.Time{}
	}

	if expire != nil {
		image.ExpiresAt = *expire
	} else {
		image.ExpiresAt = time.Time{}
	}

	if used != nil {
		image.LastUsedAt = *used
	} else {
		image.LastUsedAt = time.Time{}
	}

	image.Architecture, _ = osarch.ArchitectureName(arch)
	image.Type = instancetype.Type(imageType).String()

	// The upload date is enforced by NOT NULL in the schema, so it can never be nil.
	image.UploadedAt = *upload

	// Get the properties
	properties, err := query.SelectConfig(ctx, c.tx, "images_properties", "image_id=?", id)
	if err != nil {
		return err
	}

	image.Properties = properties

	q := "SELECT name, description FROM images_aliases WHERE image_id=?"

	// Get the aliases
	aliases := []api.ImageAlias{}
	err = query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		alias := api.ImageAlias{}

		err := scan(&alias.Name, &alias.Description)
		if err != nil {
			return err
		}

		aliases = append(aliases, alias)
		return nil
	}, id)
	if err != nil {
		return err
	}

	image.Aliases = aliases

	_, source, err := c.GetImageSource(ctx, id)
	if err == nil {
		image.UpdateSource = &source
	}

	return nil
}

func (c *ClusterTx) imageFillProfiles(ctx context.Context, id int, image *api.Image, project string) error {
	// Check which project name to use
	enabled, err := cluster.ProjectHasProfiles(context.Background(), c.tx, project)
	if err != nil {
		return fmt.Errorf("Check if project has profiles: %w", err)
	}

	if !enabled {
		project = "default"
	}

	// Get the profiles
	q := `
SELECT profiles.name FROM profiles
	JOIN images_profiles ON images_profiles.profile_id = profiles.id
	JOIN projects ON profiles.project_id = projects.id
WHERE images_profiles.image_id = ? AND projects.name = ?
`
	profiles, err := query.SelectStrings(ctx, c.tx, q, id, project)
	if err != nil {
		return err
	}

	image.Profiles = profiles

	return nil
}

// GetImagesFingerprints returns the names of all images (optionally only the public ones).
func (c *ClusterTx) GetImagesFingerprints(ctx context.Context, projectName string, publicOnly bool) ([]string, error) {
	q := `
SELECT fingerprint
  FROM images
  JOIN projects ON projects.id = images.project_id
 WHERE projects.name = ?
`
	if publicOnly {
		q += " AND public=1"
	}

	var fingerprints []string

	enabled, err := cluster.ProjectHasImages(ctx, c.tx, projectName)
	if err != nil {
		return nil, fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		projectName = "default"
	}

	fingerprints, err = query.SelectStrings(ctx, c.tx, q, projectName)
	if err != nil {
		return nil, err
	}

	return fingerprints, nil
}

// CreateImageSource inserts a new image source.
func (c *Cluster) CreateImageSource(id int, server string, protocol string, certificate string, alias string) error {
	protocolInt := -1
	for protoInt, protoString := range ImageSourceProtocol {
		if protoString == protocol {
			protocolInt = protoInt
		}
	}

	if protocolInt == -1 {
		return fmt.Errorf("Invalid protocol: %s", protocol)
	}

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := query.UpsertObject(tx.tx, "images_source", []string{
			"image_id",
			"server",
			"protocol",
			"certificate",
			"alias",
		}, []any{
			id,
			server,
			protocolInt,
			certificate,
			alias,
		})
		return err
	})

	return err
}

// GetCachedImageSourceFingerprint tries to find a source entry of a locally
// cached image that matches the given remote details (server, protocol and
// alias). Return the fingerprint linked to the matching entry, if any.
func (c *ClusterTx) GetCachedImageSourceFingerprint(ctx context.Context, server string, protocol string, alias string, typeName string, architecture int) (string, error) {
	imageType := instancetype.Any
	if typeName != "" {
		var err error
		imageType, err = instancetype.New(typeName)
		if err != nil {
			return "", err
		}
	}

	protocolInt := -1
	for protoInt, protoString := range ImageSourceProtocol {
		if protoString == protocol {
			protocolInt = protoInt
		}
	}

	if protocolInt == -1 {
		return "", fmt.Errorf("Invalid protocol: %s", protocol)
	}

	q := `SELECT images.fingerprint
			FROM images_source
			INNER JOIN images
			ON images_source.image_id=images.id
			WHERE server=? AND protocol=? AND alias=? AND auto_update=1 AND images.architecture=?
`

	args := []any{server, protocolInt, alias, architecture}
	if imageType != instancetype.Any {
		q += "AND images.type=?\n"
		args = append(args, imageType)
	}

	q += "ORDER BY creation_date DESC"

	fingerprints, err := query.SelectStrings(ctx, c.tx, q, args...)
	if err != nil {
		return "", err
	}

	if len(fingerprints) == 0 {
		return "", api.StatusErrorf(http.StatusNotFound, "Image source not found")
	}

	return fingerprints[0], nil
}

// ImageExists returns whether an image with the given fingerprint exists.
func (c *Cluster) ImageExists(project string, fingerprint string) (bool, error) {
	table := "images JOIN projects ON projects.id = images.project_id"
	where := "projects.name = ? AND fingerprint=?"

	var exists bool
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		enabled, err := cluster.ProjectHasImages(context.Background(), tx.tx, project)
		if err != nil {
			return fmt.Errorf("Check if project has images: %w", err)
		}

		if !enabled {
			project = "default"
		}

		count, err := query.Count(ctx, tx.tx, table, where, project, fingerprint)
		if err != nil {
			return err
		}

		exists = count > 0
		return nil
	})
	if err != nil {
		return false, err
	}

	return exists, nil
}

// ImageIsReferencedByOtherProjects returns true if the image with the given
// fingerprint is referenced by projects other than the given one.
func (c *Cluster) ImageIsReferencedByOtherProjects(project string, fingerprint string) (bool, error) {
	table := "images JOIN projects ON projects.id = images.project_id"
	where := "projects.name != ? AND fingerprint=?"

	var referenced bool
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		enabled, err := cluster.ProjectHasImages(context.Background(), tx.tx, project)
		if err != nil {
			return fmt.Errorf("Check if project has images: %w", err)
		}

		if !enabled {
			project = "default"
		}

		count, err := query.Count(ctx, tx.tx, table, where, project, fingerprint)
		if err != nil {
			return err
		}

		referenced = count > 0
		return nil
	})
	if err != nil {
		return false, err
	}

	return referenced, nil
}

// GetImage gets an Image object from the database.
//
// The fingerprint argument will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint. However in case the
// shortform matches more than one image, an error will be returned.
// publicOnly, when true, will return the image only if it is public;
// a false value will return any image matching the fingerprint prefix.
func (c *Cluster) GetImage(fingerprintPrefix string, filter cluster.ImageFilter) (int, *api.Image, error) {
	var image *api.Image
	var id int
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		id, image, err = tx.GetImageByFingerprintPrefix(ctx, fingerprintPrefix, filter)

		return err
	})
	if err != nil {
		return -1, nil, err
	}

	return id, image, nil
}

// GetImageByFingerprintPrefix gets an Image object from the database.
//
// The fingerprint argument will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint. However in case the
// shortform matches more than one image, an error will be returned.
// publicOnly, when true, will return the image only if it is public;
// a false value will return any image matching the fingerprint prefix.
func (c *ClusterTx) GetImageByFingerprintPrefix(ctx context.Context, fingerprintPrefix string, filter cluster.ImageFilter) (int, *api.Image, error) {
	var image api.Image
	var object cluster.Image
	if fingerprintPrefix == "" {
		return -1, nil, errors.New("No fingerprint prefix specified for the image")
	}

	if filter.Project == nil {
		return -1, nil, errors.New("No project specified for the image")
	}

	profileProject := *filter.Project
	enabled, err := cluster.ProjectHasImages(ctx, c.tx, *filter.Project)
	if err != nil {
		return -1, nil, fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		project := "default"
		filter.Project = &project
	}

	images, err := c.getImagesByFingerprintPrefix(ctx, fingerprintPrefix, filter)
	if err != nil {
		return -1, nil, fmt.Errorf("Failed to fetch images: %w", err)
	}

	switch len(images) {
	case 0:
		return -1, nil, api.StatusErrorf(http.StatusNotFound, "Image not found")
	case 1:
		object = images[0]
	default:
		return -1, nil, fmt.Errorf("More than one image matches")
	}

	image.Fingerprint = object.Fingerprint
	image.Filename = object.Filename
	image.Size = object.Size
	image.Cached = object.Cached
	image.Public = object.Public
	image.AutoUpdate = object.AutoUpdate

	err = c.imageFill(
		ctx, object.ID, &image,
		&object.CreationDate.Time, &object.ExpiryDate.Time, &object.LastUseDate.Time,
		&object.UploadDate, object.Architecture, object.Type)
	if err != nil {
		return -1, nil, fmt.Errorf("Fill image details: %w", err)
	}

	err = c.imageFillProfiles(ctx, object.ID, &image, profileProject)
	if err != nil {
		return -1, nil, fmt.Errorf("Fill image profiles: %w", err)
	}

	return object.ID, &image, nil
}

// GetImageFromAnyProject returns an image matching the given fingerprint, if
// it exists in any project.
func (c *Cluster) GetImageFromAnyProject(fingerprint string) (int, *api.Image, error) {
	// The object we'll actually return
	var image api.Image
	var object cluster.Image

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		images, err := tx.getImagesByFingerprintPrefix(ctx, fingerprint, cluster.ImageFilter{})
		if err != nil {
			return fmt.Errorf("Failed to fetch images: %w", err)
		}

		if len(images) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Image not found")
		}

		object = images[0]

		image.Fingerprint = object.Fingerprint
		image.Filename = object.Filename
		image.Size = object.Size
		image.Cached = object.Cached
		image.Public = object.Public
		image.AutoUpdate = object.AutoUpdate

		err = tx.imageFill(
			ctx, object.ID, &image,
			&object.CreationDate.Time, &object.ExpiryDate.Time, &object.LastUseDate.Time,
			&object.UploadDate, object.Architecture, object.Type)
		if err != nil {
			return fmt.Errorf("Fill image details: %w", err)
		}

		return nil
	})
	if err != nil {
		return -1, nil, fmt.Errorf("Get image %q: %w", fingerprint, err)
	}

	return object.ID, &image, nil
}

// getImagesByFingerprintPrefix returns the images with fingerprints matching the prefix.
// Optional filters 'project' and 'public' will be included if not nil.
func (c *ClusterTx) getImagesByFingerprintPrefix(ctx context.Context, fingerprintPrefix string, filter cluster.ImageFilter) ([]cluster.Image, error) {
	sql := `
SELECT images.id, projects.name AS project, images.fingerprint, images.type, images.filename, images.size, images.public, images.architecture, images.creation_date, images.expiry_date, images.upload_date, images.cached, images.last_use_date, images.auto_update
FROM images
JOIN projects ON images.project_id = projects.id
WHERE images.fingerprint LIKE ?
`
	args := []any{fingerprintPrefix + "%"}
	if filter.Project != nil {
		sql += `AND project = ?
	`
		args = append(args, *filter.Project)
	}

	if filter.Public != nil {
		sql += `AND images.public = ?
	`
		args = append(args, *filter.Public)
	}

	sql += `ORDER BY projects.id, images.fingerprint
`

	images := make([]cluster.Image, 0)

	err := query.Scan(ctx, c.Tx(), sql, func(scan func(dest ...any) error) error {
		var img cluster.Image

		err := scan(
			&img.ID,
			&img.Project,
			&img.Fingerprint,
			&img.Type,
			&img.Filename,
			&img.Size,
			&img.Public,
			&img.Architecture,
			&img.CreationDate,
			&img.ExpiryDate,
			&img.UploadDate,
			&img.Cached,
			&img.LastUseDate,
			&img.AutoUpdate,
		)
		if err != nil {
			return err
		}

		images = append(images, img)

		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch images: %w", err)
	}

	return images, nil
}

// LocateImage returns the address of an online node that has a local copy of
// the given image, or an empty string if the image is already available on this
// node.
//
// If the image is not available on any online node, an error is returned.
func (c *Cluster) LocateImage(fingerprint string) (string, error) {
	stmt := `
SELECT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
`
	var localAddress string // Address of this node
	var addresses []string  // Addresses of online nodes with the image

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		offlineThreshold, err := tx.GetNodeOfflineThreshold(ctx)
		if err != nil {
			return err
		}

		localAddress, err = tx.GetLocalNodeAddress(ctx)
		if err != nil {
			return err
		}

		allAddresses, err := query.SelectStrings(ctx, tx.tx, stmt, fingerprint)
		if err != nil {
			return err
		}

		for _, address := range allAddresses {
			node, err := tx.GetNodeByAddress(ctx, address)
			if err != nil {
				return err
			}

			if address != localAddress && node.IsOffline(offlineThreshold) {
				continue
			}

			addresses = append(addresses, address)
		}

		return err
	})
	if err != nil {
		return "", err
	}

	if len(addresses) == 0 {
		return "", fmt.Errorf("Image not available on any online member")
	}

	for _, address := range addresses {
		if address == localAddress {
			return "", nil
		}
	}

	return addresses[0], nil
}

// AddImageToLocalNode creates a new entry in the images_nodes table for
// tracking that the local member has the given image.
func (c *Cluster) AddImageToLocalNode(project, fingerprint string) error {
	imageID, _, err := c.GetImage(fingerprint, cluster.ImageFilter{Project: &project})
	if err != nil {
		return err
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.Exec("INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", imageID, c.nodeID)
		return err
	})
	return err
}

// DeleteImage deletes the image with the given ID.
func (c *Cluster) DeleteImage(id int) error {
	return c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		deleted, err := query.DeleteObject(tx.tx, "images", int64(id))
		if err != nil {
			return err
		}

		if !deleted {
			return fmt.Errorf("No image with ID %d", id)
		}

		return nil
	})
}

// GetImageAliases returns the names of the aliases of all images.
func (c *ClusterTx) GetImageAliases(ctx context.Context, projectName string) ([]string, error) {
	var names []string
	q := `
SELECT images_aliases.name
  FROM images_aliases
  JOIN projects ON projects.id=images_aliases.project_id
 WHERE projects.name=?
`

	enabled, err := cluster.ProjectHasImages(ctx, c.tx, projectName)
	if err != nil {
		return nil, fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		projectName = "default"
	}

	names, err = query.SelectStrings(ctx, c.tx, q, projectName)
	if err != nil {
		return nil, err
	}

	return names, nil
}

// GetImageAlias returns the alias with the given name in the given project.
func (c *ClusterTx) GetImageAlias(ctx context.Context, projectName string, imageName string, isTrustedClient bool) (int, api.ImageAliasesEntry, error) {
	id := -1
	entry := api.ImageAliasesEntry{}
	q := `SELECT images_aliases.id, images.fingerprint, images.type, images_aliases.description
			 FROM images_aliases
			 INNER JOIN images
			 ON images_aliases.image_id=images.id
                         INNER JOIN projects
                         ON images_aliases.project_id=projects.id
			 WHERE projects.name=? AND images_aliases.name=?`
	if !isTrustedClient {
		q = q + ` AND images.public=1`
	}

	enabled, err := cluster.ProjectHasImages(ctx, c.tx, projectName)
	if err != nil {
		return -1, api.ImageAliasesEntry{}, fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		projectName = "default"
	}

	var fingerprint, description string
	var imageType int

	arg1 := []any{projectName, imageName}
	arg2 := []any{&id, &fingerprint, &imageType, &description}
	err = c.tx.QueryRowContext(ctx, q, arg1...).Scan(arg2...)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, api.ImageAliasesEntry{}, api.StatusErrorf(http.StatusNotFound, "Image alias not found")
		}

		return 0, entry, err
	}

	entry.Name = imageName
	entry.Target = fingerprint
	entry.Description = description
	entry.Type = instancetype.Type(imageType).String()

	return id, entry, nil
}

// RenameImageAlias renames the alias with the given ID.
func (c *Cluster) RenameImageAlias(id int, name string) error {
	q := "UPDATE images_aliases SET name=? WHERE id=?"
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.Exec(q, name, id)
		return err
	})
	return err
}

// DeleteImageAlias deletes the alias with the given name.
func (c *ClusterTx) DeleteImageAlias(ctx context.Context, projectName string, name string) error {
	q := `
DELETE
  FROM images_aliases
 WHERE project_id = (SELECT id FROM projects WHERE name = ?) AND name = ?
`
	enabled, err := cluster.ProjectHasImages(ctx, c.tx, projectName)
	if err != nil {
		return fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		projectName = "default"
	}

	_, err = c.tx.ExecContext(ctx, q, projectName, name)
	if err != nil {
		return err
	}

	return nil
}

// MoveImageAlias changes the image ID associated with an alias.
func (c *Cluster) MoveImageAlias(source int, destination int) error {
	q := "UPDATE images_aliases SET image_id=? WHERE image_id=?"
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.Exec(q, destination, source)
		return err
	})
	return err
}

// CreateImageAlias inserts an alias ento the database.
func (c *ClusterTx) CreateImageAlias(ctx context.Context, projectName, aliasName string, imageID int, desc string) error {
	stmt := `INSERT INTO images_aliases (name, image_id, description, project_id)
VALUES (?, ?, ?, (SELECT id FROM projects WHERE name = ?))
`
	enabled, err := cluster.ProjectHasImages(ctx, c.tx, projectName)
	if err != nil {
		return fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		projectName = "default"
	}

	_, err = c.tx.Exec(stmt, aliasName, imageID, desc, projectName)
	if err != nil {
		return err
	}

	return nil
}

// UpdateImageAlias updates the alias with the given ID.
func (c *ClusterTx) UpdateImageAlias(ctx context.Context, aliasID int, imageID int, desc string) error {
	stmt := `UPDATE images_aliases SET image_id=?, description=? WHERE id=?`
	_, err := c.tx.ExecContext(ctx, stmt, imageID, desc, aliasID)
	return err
}

// CopyDefaultImageProfiles copies default profiles from id to new_id.
func (c *Cluster) CopyDefaultImageProfiles(id int, newID int) error {
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		// Delete all current associations.
		_, err := tx.tx.Exec("DELETE FROM images_profiles WHERE image_id=?", newID)
		if err != nil {
			return err
		}

		// Copy the entries over.
		_, err = tx.tx.Exec("INSERT INTO images_profiles (image_id, profile_id) SELECT ?, profile_id FROM images_profiles WHERE image_id=?", newID, id)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateImageLastUseDate updates the last_use_date field of the image with the
// given fingerprint.
func (c *ClusterTx) UpdateImageLastUseDate(ctx context.Context, projectName string, fingerprint string, lastUsed time.Time) error {
	stmt := `UPDATE images SET last_use_date=? WHERE fingerprint=? AND project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)`
	_, err := c.tx.ExecContext(ctx, stmt, lastUsed, fingerprint, projectName)
	return err
}

// SetImageCachedAndLastUseDate sets the cached and last_use_date field of the image with the given fingerprint.
func (c *Cluster) SetImageCachedAndLastUseDate(projectName string, fingerprint string, lastUsed time.Time) error {
	stmt := `UPDATE images SET cached=1, last_use_date=? WHERE fingerprint=? AND project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)`
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err := tx.tx.Exec(stmt, lastUsed, fingerprint, projectName)
		return err
	})
	return err
}

// UpdateImage updates the image with the given ID.
func (c *Cluster) UpdateImage(id int, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string, project string, profileIds []int64) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		publicInt := 0
		if public {
			publicInt = 1
		}

		autoUpdateInt := 0
		if autoUpdate {
			autoUpdateInt = 1
		}

		sql := `UPDATE images SET filename=?, size=?, public=?, auto_update=?, architecture=?, creation_date=?, expiry_date=? WHERE id=?`
		_, err = tx.tx.Exec(sql, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, id)
		if err != nil {
			return err
		}

		_, err = tx.tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, id)
		if err != nil {
			return err
		}

		sql = `INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`
		for key, value := range properties {
			if value == "" {
				continue
			}

			_, err = tx.tx.Exec(sql, id, 0, key, value)
			if err != nil {
				return err
			}
		}

		if project != "" && profileIds != nil {
			enabled, err := cluster.ProjectHasProfiles(context.Background(), tx.tx, project)
			if err != nil {
				return err
			}

			if !enabled {
				project = "default"
			}

			q := `DELETE FROM images_profiles
				WHERE image_id = ? AND profile_id IN (
					SELECT profiles.id FROM profiles
					JOIN projects ON profiles.project_id = projects.id
					WHERE projects.name = ?
				)`
			_, err = tx.tx.Exec(q, id, project)
			if err != nil {
				return err
			}

			sql = `INSERT INTO images_profiles (image_id, profile_id) VALUES (?, ?)`
			for _, profileID := range profileIds {
				_, err = tx.tx.Exec(sql, id, profileID)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	return err
}

// CreateImage creates a new image.
func (c *Cluster) CreateImage(project string, fp string, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string, typeName string, profileIds []int64) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	imageType := instancetype.Any
	if typeName != "" {
		var err error
		imageType, err = instancetype.New(typeName)
		if err != nil {
			return err
		}
	}

	if imageType == -1 {
		return fmt.Errorf("Invalid image type: %v", typeName)
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		imageProject := project
		enabled, err := cluster.ProjectHasImages(ctx, tx.tx, imageProject)
		if err != nil {
			return fmt.Errorf("Check if project has images: %w", err)
		}

		if !enabled {
			imageProject = "default"
		}

		publicInt := 0
		if public {
			publicInt = 1
		}

		autoUpdateInt := 0
		if autoUpdate {
			autoUpdateInt = 1
		}

		sql := `INSERT INTO images (project_id, fingerprint, filename, size, public, auto_update, architecture, creation_date, expiry_date, upload_date, type) VALUES ((SELECT id FROM projects WHERE name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		result, err := tx.tx.Exec(sql, imageProject, fp, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, time.Now().UTC(), imageType)
		if err != nil {
			return fmt.Errorf("Failed saving main image record: %w", err)
		}

		var id int
		{
			id64, err := result.LastInsertId()
			if err != nil {
				return fmt.Errorf("Failed getting image ID: %w", err)
			}

			id = int(id64)
		}

		if len(properties) > 0 {
			sql = `INSERT INTO images_properties (image_id, type, key, value) VALUES (?, 0, ?, ?)`
			for k, v := range properties {
				// we can assume, that there is just one
				// value per key
				_, err = tx.tx.Exec(sql, id, k, v)
				if err != nil {
					return fmt.Errorf("Failed saving image properties %d: %w", id, err)
				}
			}
		}

		if profileIds != nil {
			sql = `INSERT INTO images_profiles (image_id, profile_id) VALUES (?, ?)`
			for _, profileID := range profileIds {
				_, err = tx.tx.Exec(sql, id, profileID)
				if err != nil {
					return fmt.Errorf("Failed saving image profiles: %w", err)
				}
			}
		} else {
			dbProfiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), project, []string{"default"})
			if err != nil {
				return err
			}

			if len(dbProfiles) != 1 {
				return fmt.Errorf("Failed to find default profile in project %q", project)
			}

			_, err = tx.tx.Exec("INSERT INTO images_profiles(image_id, profile_id) VALUES(?, ?)", id, dbProfiles[0].ID)
			if err != nil {
				return fmt.Errorf("Failed saving image prfofiles: %w", err)
			}
		}

		// All projects with features.images=false can use all images added to the "default" project.
		// If these projects also have features.profiles=true, their default profiles should be associated
		// with all created images.
		if imageProject == "default" {
			_, err = tx.tx.Exec(
				`INSERT OR IGNORE INTO images_profiles(image_id, profile_id)
					SELECT ?, profiles.id FROM profiles
						JOIN projects_config AS t1 ON t1.project_id = profiles.project_id
							AND t1.key = "features.images"
							AND t1.value = "false"
						JOIN projects_config AS t2 ON t2.project_id = profiles.project_id
							AND t2.key = "features.profiles"
							AND t2.value = "true"
						WHERE profiles.name = "default"`, id)
			if err != nil {
				return err
			}
		}

		_, err = tx.tx.Exec("INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", id, c.nodeID)
		if err != nil {
			return fmt.Errorf("Failed saving image member info: %w", err)
		}

		return nil
	})
	return err
}

// GetPoolsWithImage get the IDs of all storage pools on which a given image exists.
func (c *Cluster) GetPoolsWithImage(imageFingerprint string) ([]int64, error) {
	q := "SELECT storage_pool_id FROM storage_volumes WHERE (node_id=? OR node_id IS NULL) AND name=? AND type=?"
	var ids []int
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		ids, err = query.SelectIntegers(ctx, tx.tx, q, c.nodeID, imageFingerprint, StoragePoolVolumeTypeImage)
		return err
	})
	if err != nil {
		return nil, err
	}

	poolIDs := make([]int64, len(ids))
	for i, id := range ids {
		poolIDs[i] = int64(id)
	}

	return poolIDs, nil
}

// GetPoolNamesFromIDs get the names of the storage pools with the given IDs.
func (c *Cluster) GetPoolNamesFromIDs(poolIDs []int64) ([]string, error) {
	params := make([]string, len(poolIDs))
	args := make([]any, len(poolIDs))
	for i, id := range poolIDs {
		params[i] = "?"
		args[i] = id
	}

	q := fmt.Sprintf("SELECT name FROM storage_pools WHERE id IN (%s)", strings.Join(params, ","))

	var poolNames []string

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		poolNames, err = query.SelectStrings(ctx, tx.tx, q, args...)
		return err
	})
	if err != nil {
		return nil, err
	}

	if len(poolNames) != len(poolIDs) {
		return nil, fmt.Errorf("Found only %d matches, expected %d", len(poolNames), len(poolIDs))
	}

	return poolNames, nil
}

// GetImages returns all images.
func (c *Cluster) GetImages() (map[string][]string, error) {
	images := make(map[string][]string) // key is fingerprint, value is list of projects
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		stmt := `
    SELECT images.fingerprint, projects.name FROM images
      LEFT JOIN projects ON images.project_id = projects.id
		`
		rows, err := tx.tx.QueryContext(ctx, stmt)
		if err != nil {
			return err
		}

		var fingerprint string
		var projectName string
		for rows.Next() {
			err := rows.Scan(&fingerprint, &projectName)
			if err != nil {
				return err
			}

			images[fingerprint] = append(images[fingerprint], projectName)
		}

		return rows.Err()
	})
	return images, err
}

// GetImagesOnLocalNode returns all images that the local server holds.
func (c *Cluster) GetImagesOnLocalNode() (map[string][]string, error) {
	return c.GetImagesOnNode(c.nodeID)
}

// GetImagesOnNode returns all images that the node with the given id has.
func (c *Cluster) GetImagesOnNode(id int64) (map[string][]string, error) {
	images := make(map[string][]string) // key is fingerprint, value is list of projects
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		stmt := `
    SELECT images.fingerprint, projects.name FROM images
      LEFT JOIN images_nodes ON images.id = images_nodes.image_id
			LEFT JOIN nodes ON images_nodes.node_id = nodes.id
			LEFT JOIN projects ON images.project_id = projects.id
    WHERE nodes.id = ?
		`
		rows, err := tx.tx.QueryContext(ctx, stmt, id)
		if err != nil {
			return err
		}

		var fingerprint string
		var projectName string
		for rows.Next() {
			err := rows.Scan(&fingerprint, &projectName)
			if err != nil {
				return err
			}

			images[fingerprint] = append(images[fingerprint], projectName)
		}

		return rows.Err()
	})
	return images, err
}

// GetNodesWithImage returns the addresses of online nodes which already have the image.
func (c *Cluster) GetNodesWithImage(fingerprint string) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
	`
	return c.getNodesByImageFingerprint(q, fingerprint, nil)
}

// GetNodesWithImageAndAutoUpdate returns the addresses of online nodes which already have the image.
func (c *Cluster) GetNodesWithImageAndAutoUpdate(fingerprint string, autoUpdate bool) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes
  JOIN images_nodes ON images_nodes.node_id = nodes.id
  JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ? AND images.auto_update = ?
	`
	return c.getNodesByImageFingerprint(q, fingerprint, &autoUpdate)
}

// GetNodesWithoutImage returns the addresses of online nodes which don't have the image.
func (c *Cluster) GetNodesWithoutImage(fingerprint string) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes WHERE nodes.address NOT IN (
  SELECT DISTINCT nodes.address FROM nodes
    LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
    LEFT JOIN images ON images_nodes.image_id = images.id
  WHERE images.fingerprint = ?)
`
	return c.getNodesByImageFingerprint(q, fingerprint, nil)
}

func (c *Cluster) getNodesByImageFingerprint(stmt string, fingerprint string, autoUpdate *bool) ([]string, error) {
	var addresses []string // Addresses of online nodes with the image
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		offlineThreshold, err := tx.GetNodeOfflineThreshold(ctx)
		if err != nil {
			return err
		}

		var allAddresses []string

		if autoUpdate == nil {
			allAddresses, err = query.SelectStrings(ctx, tx.tx, stmt, fingerprint)
		} else {
			allAddresses, err = query.SelectStrings(ctx, tx.tx, stmt, fingerprint, autoUpdate)
		}

		if err != nil {
			return err
		}

		for _, address := range allAddresses {
			node, err := tx.GetNodeByAddress(ctx, address)
			if err != nil {
				return err
			}

			if node.IsOffline(offlineThreshold) {
				continue
			}

			addresses = append(addresses, address)
		}

		return err
	})
	return addresses, err
}

// GetProjectsUsingImage get the project names using an image by fingerprint.
func (c *ClusterTx) GetProjectsUsingImage(ctx context.Context, fingerprint string) ([]string, error) {
	var err error
	var imgProjectNames []string

	q := `
		SELECT projects.name
		FROM images
		JOIN projects ON projects.id=images.project_id
		WHERE fingerprint = ?
	`
	err = query.Scan(ctx, c.Tx(), q, func(scan func(dest ...any) error) error {
		var imgProjectName string
		err = scan(&imgProjectName)
		if err != nil {
			return err
		}

		imgProjectNames = append(imgProjectNames, imgProjectName)

		return nil
	}, fingerprint)
	if err != nil {
		return nil, err
	}

	return imgProjectNames, nil
}
