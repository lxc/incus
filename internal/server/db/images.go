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

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/osarch"
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
func (c *ClusterTx) CreateImageSource(ctx context.Context, id int, server string, protocol string, certificate string, alias string) error {
	protocolInt := -1
	for protoInt, protoString := range ImageSourceProtocol {
		if protoString == protocol {
			protocolInt = protoInt
		}
	}

	if protocolInt == -1 {
		return fmt.Errorf("Invalid protocol: %s", protocol)
	}

	_, err := query.UpsertObject(c.tx, "images_source", []string{
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
func (c *ClusterTx) ImageExists(ctx context.Context, project string, fingerprint string) (bool, error) {
	table := "images JOIN projects ON projects.id = images.project_id"
	where := "projects.name = ? AND fingerprint=?"

	enabled, err := cluster.ProjectHasImages(ctx, c.tx, project)
	if err != nil {
		return false, fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		project = "default"
	}

	count, err := query.Count(ctx, c.tx, table, where, project, fingerprint)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// ImageIsReferencedByOtherProjects returns true if the image with the given
// fingerprint is referenced by projects other than the given one.
func (c *ClusterTx) ImageIsReferencedByOtherProjects(ctx context.Context, project string, fingerprint string) (bool, error) {
	table := "images JOIN projects ON projects.id = images.project_id"
	where := "projects.name != ? AND fingerprint=?"

	enabled, err := cluster.ProjectHasImages(ctx, c.tx, project)
	if err != nil {
		return false, fmt.Errorf("Check if project has images: %w", err)
	}

	if !enabled {
		project = "default"
	}

	count, err := query.Count(ctx, c.tx, table, where, project, fingerprint)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// GetImage gets an Image object from the database.
//
// The fingerprint argument will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint. However in case the
// shortform matches more than one image, an error will be returned.
// publicOnly, when true, will return the image only if it is public;
// a false value will return any image matching the fingerprint prefix.
func (c *ClusterTx) GetImage(ctx context.Context, fingerprintPrefix string, filter cluster.ImageFilter) (int, *api.Image, error) {
	id, image, err := c.GetImageByFingerprintPrefix(ctx, fingerprintPrefix, filter)
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
	image.Project = object.Project

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
func (c *ClusterTx) GetImageFromAnyProject(ctx context.Context, fingerprint string) (int, *api.Image, error) {
	// The object we'll actually return
	var image api.Image
	var object cluster.Image

	images, err := c.getImagesByFingerprintPrefix(ctx, fingerprint, cluster.ImageFilter{})
	if err != nil {
		return -1, nil, fmt.Errorf("Get image %q: Failed to fetch images: %w", fingerprint, err)
	}

	if len(images) == 0 {
		return -1, nil, fmt.Errorf("Get image %q: %w", fingerprint, api.StatusErrorf(http.StatusNotFound, "Image not found"))
	}

	object = images[0]

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
		return -1, nil, fmt.Errorf("Get image %q: Fill image details: %w", fingerprint, err)
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
func (c *ClusterTx) LocateImage(ctx context.Context, fingerprint string) (string, error) {
	stmt := `
SELECT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
`
	var localAddress string // Address of this node
	var addresses []string  // Addresses of online nodes with the image

	offlineThreshold, err := c.GetNodeOfflineThreshold(ctx)
	if err != nil {
		return "", err
	}

	localAddress, err = c.GetLocalNodeAddress(ctx)
	if err != nil {
		return "", err
	}

	allAddresses, err := query.SelectStrings(ctx, c.tx, stmt, fingerprint)
	if err != nil {
		return "", err
	}

	for _, address := range allAddresses {
		node, err := c.GetNodeByAddress(ctx, address)
		if err != nil {
			return "", err
		}

		if address != localAddress && node.IsOffline(offlineThreshold) {
			continue
		}

		addresses = append(addresses, address)
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
func (c *ClusterTx) AddImageToLocalNode(ctx context.Context, project, fingerprint string) error {
	imageID, _, err := c.GetImage(ctx, fingerprint, cluster.ImageFilter{Project: &project})
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", imageID, c.nodeID)

	return err
}

// DeleteImage deletes the image with the given ID.
func (c *ClusterTx) DeleteImage(ctx context.Context, id int) error {
	deleted, err := query.DeleteObject(c.tx, "images", int64(id))
	if err != nil {
		return err
	}

	if !deleted {
		return fmt.Errorf("No image with ID %d", id)
	}

	return nil
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
func (c *ClusterTx) RenameImageAlias(ctx context.Context, id int, name string) error {
	q := "UPDATE images_aliases SET name=? WHERE id=?"
	_, err := c.tx.ExecContext(ctx, q, name, id)

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
func (c *ClusterTx) MoveImageAlias(ctx context.Context, source int, destination int) error {
	q := "UPDATE images_aliases SET image_id=? WHERE image_id=?"
	_, err := c.tx.ExecContext(ctx, q, destination, source)

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
func (c *ClusterTx) CopyDefaultImageProfiles(ctx context.Context, id int, newID int) error {
	// Delete all current associations.
	_, err := c.tx.ExecContext(ctx, "DELETE FROM images_profiles WHERE image_id=?", newID)
	if err != nil {
		return err
	}

	// Copy the entries over.
	_, err = c.tx.ExecContext(ctx, "INSERT INTO images_profiles (image_id, profile_id) SELECT ?, profile_id FROM images_profiles WHERE image_id=?", newID, id)
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
func (c *ClusterTx) SetImageCachedAndLastUseDate(ctx context.Context, projectName string, fingerprint string, lastUsed time.Time) error {
	stmt := `UPDATE images SET cached=1, last_use_date=? WHERE fingerprint=? AND project_id = (SELECT id FROM projects WHERE name = ? LIMIT 1)`

	_, err := c.tx.ExecContext(ctx, stmt, lastUsed, fingerprint, projectName)

	return err
}

// UpdateImage updates the image with the given ID.
func (c *ClusterTx) UpdateImage(ctx context.Context, id int, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string, project string, profileIds []int64) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	publicInt := 0
	if public {
		publicInt = 1
	}

	autoUpdateInt := 0
	if autoUpdate {
		autoUpdateInt = 1
	}

	sql := `UPDATE images SET filename=?, size=?, public=?, auto_update=?, architecture=?, creation_date=?, expiry_date=? WHERE id=?`
	_, err = c.tx.ExecContext(ctx, sql, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, id)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, `DELETE FROM images_properties WHERE image_id=?`, id)
	if err != nil {
		return err
	}

	sql = `INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`
	for key, value := range properties {
		if value == "" {
			continue
		}

		_, err = c.tx.ExecContext(ctx, sql, id, 0, key, value)
		if err != nil {
			return err
		}
	}

	if project != "" && profileIds != nil {
		enabled, err := cluster.ProjectHasProfiles(ctx, c.tx, project)
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
		_, err = c.tx.ExecContext(ctx, q, id, project)
		if err != nil {
			return err
		}

		sql = `INSERT INTO images_profiles (image_id, profile_id) VALUES (?, ?)`
		for _, profileID := range profileIds {
			_, err = c.tx.ExecContext(ctx, sql, id, profileID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// CreateImage creates a new image.
func (c *ClusterTx) CreateImage(ctx context.Context, project string, fp string, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string, typeName string, profileIds []int64) error {
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

	imageProject := project
	enabled, err := cluster.ProjectHasImages(ctx, c.tx, imageProject)
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
	result, err := c.tx.ExecContext(ctx, sql, imageProject, fp, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, time.Now().UTC(), imageType)
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
			_, err = c.tx.ExecContext(ctx, sql, id, k, v)
			if err != nil {
				return fmt.Errorf("Failed saving image properties %d: %w", id, err)
			}
		}
	}

	if profileIds != nil {
		sql = `INSERT INTO images_profiles (image_id, profile_id) VALUES (?, ?)`
		for _, profileID := range profileIds {
			_, err = c.tx.ExecContext(ctx, sql, id, profileID)
			if err != nil {
				return fmt.Errorf("Failed saving image profiles: %w", err)
			}
		}
	} else {
		dbProfiles, err := cluster.GetProfilesIfEnabled(ctx, c.tx, project, []string{"default"})
		if err != nil {
			return err
		}

		if len(dbProfiles) != 1 {
			return fmt.Errorf("Failed to find default profile in project %q", project)
		}

		_, err = c.tx.ExecContext(ctx, "INSERT INTO images_profiles(image_id, profile_id) VALUES(?, ?)", id, dbProfiles[0].ID)
		if err != nil {
			return fmt.Errorf("Failed saving image prfofiles: %w", err)
		}
	}

	// All projects with features.images=false can use all images added to the "default" project.
	// If these projects also have features.profiles=true, their default profiles should be associated
	// with all created images.
	if imageProject == "default" {
		_, err = c.tx.ExecContext(ctx,
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

	_, err = c.tx.ExecContext(ctx, "INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", id, c.nodeID)
	if err != nil {
		return fmt.Errorf("Failed saving image member info: %w", err)
	}

	return nil
}

// GetPoolsWithImage get the IDs of all storage pools on which a given image exists.
func (c *ClusterTx) GetPoolsWithImage(ctx context.Context, imageFingerprint string) ([]int64, error) {
	q := "SELECT storage_pool_id FROM storage_volumes WHERE (node_id=? OR node_id IS NULL) AND name=? AND type=?"

	ids, err := query.SelectIntegers(ctx, c.tx, q, c.nodeID, imageFingerprint, StoragePoolVolumeTypeImage)
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
func (c *ClusterTx) GetPoolNamesFromIDs(ctx context.Context, poolIDs []int64) ([]string, error) {
	params := make([]string, len(poolIDs))
	args := make([]any, len(poolIDs))
	for i, id := range poolIDs {
		params[i] = "?"
		args[i] = id
	}

	q := fmt.Sprintf("SELECT name FROM storage_pools WHERE id IN (%s)", strings.Join(params, ","))

	poolNames, err := query.SelectStrings(ctx, c.tx, q, args...)
	if err != nil {
		return nil, err
	}

	if len(poolNames) != len(poolIDs) {
		return nil, fmt.Errorf("Found only %d matches, expected %d", len(poolNames), len(poolIDs))
	}

	return poolNames, nil
}

// GetImages returns all images.
func (c *ClusterTx) GetImages(ctx context.Context) (map[string][]string, error) {
	images := make(map[string][]string) // key is fingerprint, value is list of projects

	stmt := `
    SELECT images.fingerprint, projects.name FROM images
      LEFT JOIN projects ON images.project_id = projects.id
		`
	rows, err := c.tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}

	var fingerprint string
	var projectName string
	for rows.Next() {
		err := rows.Scan(&fingerprint, &projectName)
		if err != nil {
			return nil, err
		}

		images[fingerprint] = append(images[fingerprint], projectName)
	}

	return images, rows.Err()
}

// GetImagesOnLocalNode returns all images that the local server holds.
func (c *ClusterTx) GetImagesOnLocalNode(ctx context.Context) (map[string][]string, error) {
	return c.GetImagesOnNode(ctx, c.nodeID)
}

// GetImagesOnNode returns all images that the node with the given id has.
func (c *ClusterTx) GetImagesOnNode(ctx context.Context, id int64) (map[string][]string, error) {
	images := make(map[string][]string) // key is fingerprint, value is list of projects

	stmt := `
    SELECT images.fingerprint, projects.name FROM images
      LEFT JOIN images_nodes ON images.id = images_nodes.image_id
			LEFT JOIN nodes ON images_nodes.node_id = nodes.id
			LEFT JOIN projects ON images.project_id = projects.id
    WHERE nodes.id = ?
		`
	rows, err := c.tx.QueryContext(ctx, stmt, id)
	if err != nil {
		return nil, err
	}

	var fingerprint string
	var projectName string
	for rows.Next() {
		err := rows.Scan(&fingerprint, &projectName)
		if err != nil {
			return nil, err
		}

		images[fingerprint] = append(images[fingerprint], projectName)
	}

	return images, rows.Err()
}

// GetNodesWithImage returns the addresses of online nodes which already have the image.
func (c *ClusterTx) GetNodesWithImage(ctx context.Context, fingerprint string) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
	`
	return c.getNodesByImageFingerprint(ctx, q, fingerprint, nil)
}

// GetNodesWithImageAndAutoUpdate returns the addresses of online nodes which already have the image.
func (c *ClusterTx) GetNodesWithImageAndAutoUpdate(ctx context.Context, fingerprint string, autoUpdate bool) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes
  JOIN images_nodes ON images_nodes.node_id = nodes.id
  JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ? AND images.auto_update = ?
	`
	return c.getNodesByImageFingerprint(ctx, q, fingerprint, &autoUpdate)
}

// GetNodesWithoutImage returns the addresses of online nodes which don't have the image.
func (c *ClusterTx) GetNodesWithoutImage(ctx context.Context, fingerprint string) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes WHERE nodes.address NOT IN (
  SELECT DISTINCT nodes.address FROM nodes
    LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
    LEFT JOIN images ON images_nodes.image_id = images.id
  WHERE images.fingerprint = ?)
`
	return c.getNodesByImageFingerprint(ctx, q, fingerprint, nil)
}

func (c *ClusterTx) getNodesByImageFingerprint(ctx context.Context, stmt string, fingerprint string, autoUpdate *bool) ([]string, error) {
	var addresses []string // Addresses of online nodes with the image

	offlineThreshold, err := c.GetNodeOfflineThreshold(ctx)
	if err != nil {
		return nil, err
	}

	var allAddresses []string

	if autoUpdate == nil {
		allAddresses, err = query.SelectStrings(ctx, c.tx, stmt, fingerprint)
	} else {
		allAddresses, err = query.SelectStrings(ctx, c.tx, stmt, fingerprint, autoUpdate)
	}

	if err != nil {
		return nil, err
	}

	for _, address := range allAddresses {
		node, err := c.GetNodeByAddress(ctx, address)
		if err != nil {
			return nil, err
		}

		if node.IsOffline(offlineThreshold) {
			continue
		}

		addresses = append(addresses, address)
	}

	return addresses, nil
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
