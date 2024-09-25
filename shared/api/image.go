package api

import (
	"time"
)

// ImageExportPost represents the fields required to export an image
//
// swagger:model
//
// API extension: images_push_relay.
type ImageExportPost struct {
	// Target server URL
	// Example: https://1.2.3.4:8443
	Target string `json:"target" yaml:"target"`

	// Image receive secret
	// Example: RANDOM-STRING
	Secret string `json:"secret" yaml:"secret"`

	// Remote server certificate
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// List of aliases to set on the image
	Aliases []ImageAlias `json:"aliases" yaml:"aliases"`

	// Project name
	// Example: project1
	//
	// API extension: image_target_project
	Project string `json:"project" yaml:"project"`

	// List of profiles to use
	// Example: ["default"]
	//
	// API extension: image_copy_profile
	Profiles []string `json:"profiles" yaml:"profiles"`
}

// ImagesPost represents the fields available for a new image
//
// swagger:model
type ImagesPost struct {
	ImagePut `yaml:",inline"`

	// Original filename of the image
	// Example: image.tar.xz
	Filename string `json:"filename" yaml:"filename"`

	// Source of the image
	Source *ImagesPostSource `json:"source" yaml:"source"`

	// Compression algorithm to use when turning an instance into an image
	// Example: gzip
	//
	// API extension: image_compression_algorithm
	CompressionAlgorithm string `json:"compression_algorithm" yaml:"compression_algorithm"`

	// Aliases to add to the image
	// Example: [{"name": "foo"}, {"name": "bar"}]
	//
	// API extension: image_create_aliases
	Aliases []ImageAlias `json:"aliases" yaml:"aliases"`
}

// ImagesPostSource represents the source of a new image
//
// swagger:model
type ImagesPostSource struct {
	ImageSource `yaml:",inline"`

	// Transfer mode (push or pull)
	// Example: pull
	Mode string `json:"mode" yaml:"mode"`

	// Type of image source (instance, snapshot, image or url)
	// Example: instance
	Type string `json:"type" yaml:"type"`

	// Source URL (for type "url")
	// Example: https://some-server.com/some-directory/
	URL string `json:"url" yaml:"url"`

	// Instance name (for type "instance" or "snapshot")
	// Example: c1/snap0
	Name string `json:"name" yaml:"name"`

	// Source image fingerprint (for type "image")
	// Example: 8ae945c52bb2f2df51c923b04022312f99bbb72c356251f54fa89ea7cf1df1d0
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`

	// Source image server secret token (when downloading private images)
	// Example: RANDOM-STRING
	Secret string `json:"secret" yaml:"secret"`

	// Source project name
	// Example: project1
	//
	// API extension: image_source_project
	Project string `json:"project" yaml:"project"`
}

// ImagePut represents the modifiable fields of an image
//
// swagger:model
type ImagePut struct {
	// Whether the image should auto-update when a new build is available
	// Example: true
	AutoUpdate bool `json:"auto_update" yaml:"auto_update"`

	// Descriptive properties
	// Example: {"os": "Ubuntu", "release": "jammy", "variant": "cloud"}
	Properties map[string]string `json:"properties" yaml:"properties"`

	// Whether the image is available to unauthenticated users
	// Example: false
	Public bool `json:"public" yaml:"public"`

	// When the image becomes obsolete
	// Example: 2025-03-23T20:00:00-04:00
	//
	// API extension: images_expiry
	ExpiresAt time.Time `json:"expires_at" yaml:"expires_at"`

	// List of profiles to use when creating from this image (if none provided by user)
	// Example: ["default"]
	//
	// API extension: image_profiles
	Profiles []string `json:"profiles" yaml:"profiles"`
}

// Image represents an image
//
// swagger:model
type Image struct {
	ImagePut `yaml:",inline"`

	// List of aliases
	Aliases []ImageAlias `json:"aliases" yaml:"aliases"`

	// Architecture
	// Example: x86_64
	Architecture string `json:"architecture" yaml:"architecture"`

	// Whether the image is an automatically cached remote image
	// Example: true
	Cached bool `json:"cached" yaml:"cached"`

	// Original filename
	// Example: 06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb.rootfs
	Filename string `json:"filename" yaml:"filename"`

	// Full SHA-256 fingerprint
	// Example: 06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`

	// Size of the image in bytes
	// Example: 272237676
	Size int64 `json:"size" yaml:"size"`

	// Where the image came from
	UpdateSource *ImageSource `json:"update_source,omitempty" yaml:"update_source,omitempty"`

	// Type of image (container or virtual-machine)
	// Example: container
	//
	// API extension: image_types
	Type string `json:"type" yaml:"type"`

	// When the image was originally created
	// Example: 2021-03-23T20:00:00-04:00
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// Last time the image was used
	// Example: 2021-03-22T20:39:00.575185384-04:00
	LastUsedAt time.Time `json:"last_used_at" yaml:"last_used_at"`

	// When the image was added to this server
	// Example: 2021-03-24T14:18:15.115036787-04:00
	UploadedAt time.Time `json:"uploaded_at" yaml:"uploaded_at"`

	// Project name
	// Example: project1
	//
	// API extension: images_all_projects
	Project string `json:"project" yaml:"project"`
}

// Writable converts a full Image struct into a ImagePut struct (filters read-only fields).
func (img *Image) Writable() ImagePut {
	return img.ImagePut
}

// URL returns the URL for the image.
func (img *Image) URL(apiVersion string, project string) *URL {
	return NewURL().Path(apiVersion, "images", img.Fingerprint).Project(project)
}

// ImageAlias represents an alias from the alias list of an image
//
// swagger:model
type ImageAlias struct {
	// Name of the alias
	// Example: ubuntu-22.04
	Name string `json:"name" yaml:"name"`

	// Description of the alias
	// Example: Our preferred Ubuntu image
	Description string `json:"description" yaml:"description"`
}

// ImageSource represents the source of an image
//
// swagger:model
type ImageSource struct {
	// Source alias to download from
	// Example: jammy
	Alias string `json:"alias" yaml:"alias"`

	// Source server certificate (if not trusted by system CA)
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Source server protocol
	// Example: simplestreams
	Protocol string `json:"protocol" yaml:"protocol"`

	// URL of the source server
	// Example: https://images.linuxcontainers.org
	Server string `json:"server" yaml:"server"`

	// Type of image (container or virtual-machine)
	// Example: container
	//
	// API extension: image_types
	ImageType string `json:"image_type" yaml:"image_type"`
}

// ImageAliasesPost represents a new image alias
//
// swagger:model
type ImageAliasesPost struct {
	ImageAliasesEntry `yaml:",inline"`
}

// ImageAliasesEntryPost represents the required fields to rename an image alias
//
// swagger:model
type ImageAliasesEntryPost struct {
	// Alias name
	// Example: ubuntu-22.04
	Name string `json:"name" yaml:"name"`
}

// ImageAliasesEntryPut represents the modifiable fields of an image alias
//
// swagger:model
type ImageAliasesEntryPut struct {
	// Alias description
	// Example: Our preferred Ubuntu image
	Description string `json:"description" yaml:"description"`

	// Target fingerprint for the alias
	// Example: 06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb
	Target string `json:"target" yaml:"target"`
}

// ImageAliasesEntry represents an image alias
//
// swagger:model
type ImageAliasesEntry struct {
	ImageAliasesEntryPut `yaml:",inline"`

	// Alias name
	// Example: ubuntu-22.04
	Name string `json:"name" yaml:"name"`

	// Alias type (container or virtual-machine)
	// Example: container
	//
	// API extension: image_types
	Type string `json:"type" yaml:"type"`
}

// ImageMetadata represents image metadata (used in image tarball)
//
// swagger:model
type ImageMetadata struct {
	// Architecture name
	// Example: x86_64
	Architecture string `json:"architecture" yaml:"architecture"`

	// Image creation data (as UNIX epoch)
	// Example: 1620655439
	CreationDate int64 `json:"creation_date" yaml:"creation_date"`

	// Image expiry data (as UNIX epoch)
	// Example: 1620685757
	ExpiryDate int64 `json:"expiry_date" yaml:"expiry_date"`

	// Descriptive properties
	// Example: {"os": "Ubuntu", "release": "jammy", "variant": "cloud"}
	Properties map[string]string `json:"properties" yaml:"properties"`

	// Template for files in the image
	Templates map[string]*ImageMetadataTemplate `json:"templates" yaml:"templates"`
}

// ImageMetadataTemplate represents a template entry in image metadata (used in image tarball)
//
// swagger:model
type ImageMetadataTemplate struct {
	// When to trigger the template (create, copy or start)
	// Example: create
	When []string `json:"when" yaml:"when"`

	// Whether to trigger only if the file is missing
	// Example: false
	CreateOnly bool `json:"create_only" yaml:"create_only"`

	// The template itself as a valid pongo2 template
	// Example: pongo2-template
	Template string `json:"template" yaml:"template"`

	// Key/value properties to pass to the template
	// Example: {"foo": "bar"}
	Properties map[string]string `json:"properties" yaml:"properties"`

	// The file owner uid.
	// Example: 1000
	//
	// API extension: image_template_permissions
	UID string `json:"uid,omitempty" yaml:"uid,omitempty"`

	// The file owner gid.
	// Example: 1000
	//
	// API extension: image_template_permissions
	GID string `json:"gid,omitempty" yaml:"gid,omitempty"`

	// The file permissions.
	// Example: 644
	//
	// API extension: image_template_permissions
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
}
