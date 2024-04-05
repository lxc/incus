package simplestreams

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/osarch"
)

// Products represents the base of download.json.
type Products struct {
	ContentID string             `json:"content_id"`
	DataType  string             `json:"datatype"`
	Format    string             `json:"format"`
	License   string             `json:"license,omitempty"`
	Products  map[string]Product `json:"products"`
	Updated   string             `json:"updated,omitempty"`
}

// Product represents a single product inside download.json.
type Product struct {
	Aliases         string                    `json:"aliases"`
	Architecture    string                    `json:"arch"`
	OperatingSystem string                    `json:"os"`
	Requirements    map[string]string         `json:"requirements,omitempty"`
	Release         string                    `json:"release"`
	ReleaseCodename string                    `json:"release_codename,omitempty"`
	ReleaseTitle    string                    `json:"release_title"`
	Supported       bool                      `json:"supported,omitempty"`
	SupportedEOL    string                    `json:"support_eol,omitempty"`
	Version         string                    `json:"version,omitempty"`
	Versions        map[string]ProductVersion `json:"versions"`

	// Non-standard fields (only used on some image servers).
	Variant string `json:"variant,omitempty"`
}

// ProductVersion represents a particular version of a product.
type ProductVersion struct {
	Items      map[string]ProductVersionItem `json:"items"`
	Label      string                        `json:"label,omitempty"`
	PublicName string                        `json:"pubname,omitempty"`
}

// ProductVersionItem represents a file/item of a particular ProductVersion.
type ProductVersionItem struct {
	CombinedSha256DiskImg     string `json:"combined_disk1-img_sha256,omitempty"`
	CombinedSha256DiskKvmImg  string `json:"combined_disk-kvm-img_sha256,omitempty"`
	CombinedSha256DiskUefiImg string `json:"combined_uefi1-img_sha256,omitempty"`
	CombinedSha256RootXz      string `json:"combined_rootxz_sha256,omitempty"`
	CombinedSha256            string `json:"combined_sha256,omitempty"`
	CombinedSha256SquashFs    string `json:"combined_squashfs_sha256,omitempty"`
	FileType                  string `json:"ftype"`
	HashMd5                   string `json:"md5,omitempty"`
	Path                      string `json:"path"`
	HashSha256                string `json:"sha256,omitempty"`
	Size                      int64  `json:"size"`
	DeltaBase                 string `json:"delta_base,omitempty"`
}

// ToAPI converts the products data into a list of API images and associated downloadable files.
func (s *Products) ToAPI() ([]api.Image, map[string][][]string) {
	downloads := map[string][][]string{}

	images := []api.Image{}
	nameLayout := "20060102"
	eolLayout := "2006-01-02"

	for _, product := range s.Products {
		// Skip unsupported architectures
		architecture, err := osarch.ArchitectureId(product.Architecture)
		if err != nil {
			continue
		}

		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			continue
		}

		for name, version := range product.Versions {
			// Short of anything better, use the name as date (see format above)
			if len(name) < 8 {
				continue
			}

			creationDate, err := time.Parse(nameLayout, name[0:8])
			if err != nil {
				continue
			}

			// Image processing function
			addImage := func(meta *ProductVersionItem, root *ProductVersionItem) error {
				// Look for deltas
				deltas := []ProductVersionItem{}
				if root != nil && slices.Contains([]string{"squashfs", "disk-kvm.img"}, root.FileType) {
					for _, item := range version.Items {
						if item.FileType == fmt.Sprintf("%s.vcdiff", root.FileType) {
							deltas = append(deltas, item)
						}
					}
				}

				// Figure out the fingerprint
				fingerprint := ""
				if root != nil {
					if root.FileType == "root.tar.xz" {
						if meta.CombinedSha256RootXz != "" {
							fingerprint = meta.CombinedSha256RootXz
						} else {
							fingerprint = meta.CombinedSha256
						}
					} else if root.FileType == "squashfs" {
						fingerprint = meta.CombinedSha256SquashFs
					} else if root.FileType == "disk-kvm.img" {
						fingerprint = meta.CombinedSha256DiskKvmImg
					} else if root.FileType == "disk1.img" {
						fingerprint = meta.CombinedSha256DiskImg
					} else if root.FileType == "uefi1.img" {
						fingerprint = meta.CombinedSha256DiskUefiImg
					}
				} else {
					fingerprint = meta.HashSha256
				}

				if fingerprint == "" {
					return fmt.Errorf("No image fingerprint found")
				}

				// Figure out the size
				size := meta.Size
				if root != nil {
					size += root.Size
				}

				// Determine filename
				if meta.Path == "" {
					return fmt.Errorf("Missing path field on metadata entry")
				}

				fields := strings.Split(meta.Path, "/")
				filename := fields[len(fields)-1]

				// Generate the actual image entry
				description := fmt.Sprintf("%s %s %s", product.OperatingSystem, product.ReleaseTitle, product.Architecture)
				if version.Label != "" {
					description = fmt.Sprintf("%s (%s)", description, version.Label)
				}

				description = fmt.Sprintf("%s (%s)", description, name)

				image := api.Image{}
				image.Architecture = architectureName
				image.Public = true
				image.Size = size
				image.CreatedAt = creationDate
				image.UploadedAt = creationDate
				image.Filename = filename
				image.Fingerprint = fingerprint
				image.Properties = map[string]string{
					"os":           product.OperatingSystem,
					"release":      product.Release,
					"version":      product.Version,
					"architecture": product.Architecture,
					"label":        version.Label,
					"serial":       name,
					"description":  description,
				}

				for key, value := range product.Requirements {
					image.Properties["requirements."+key] = value
				}

				if product.Variant != "" {
					image.Properties["variant"] = product.Variant
				}

				image.Type = "container"

				if root != nil {
					image.Properties["type"] = root.FileType
					if root.FileType == "disk1.img" || root.FileType == "disk-kvm.img" || root.FileType == "uefi1.img" {
						image.Type = "virtual-machine"
					}
				} else {
					image.Properties["type"] = "tar.gz"
				}

				// Clear unset properties
				for k, v := range image.Properties {
					if v == "" {
						delete(image.Properties, k)
					}
				}

				// Add the provided aliases
				if product.Aliases != "" {
					image.Aliases = []api.ImageAlias{}
					for _, entry := range strings.Split(product.Aliases, ",") {
						image.Aliases = append(image.Aliases, api.ImageAlias{Name: entry})
					}
				}

				// Attempt to parse the EOL
				image.ExpiresAt = time.Unix(0, 0).UTC()
				if product.SupportedEOL != "" {
					eolDate, err := time.Parse(eolLayout, product.SupportedEOL)
					if err == nil {
						image.ExpiresAt = eolDate
					}
				}

				// Set the file list
				var imgDownloads [][]string
				if root == nil {
					imgDownloads = [][]string{{meta.Path, meta.HashSha256, "meta", fmt.Sprintf("%d", meta.Size)}}
				} else {
					imgDownloads = [][]string{
						{meta.Path, meta.HashSha256, "meta", fmt.Sprintf("%d", meta.Size)},
						{root.Path, root.HashSha256, "root", fmt.Sprintf("%d", root.Size)}}
				}

				// Add the deltas
				for _, delta := range deltas {
					srcImage, ok := product.Versions[delta.DeltaBase]
					if !ok {
						// Delta for a since expired image
						continue
					}

					// Locate source image fingerprint
					var srcFingerprint string
					for _, item := range srcImage.Items {
						if item.FileType != "incus.tar.xz" {
							continue
						}

						srcFingerprint = item.CombinedSha256SquashFs
						break
					}

					if srcFingerprint == "" {
						// Couldn't find the image
						continue
					}

					// Add the delta
					imgDownloads = append(imgDownloads, []string{
						delta.Path,
						delta.HashSha256,
						fmt.Sprintf("root.delta-%s", srcFingerprint),
						fmt.Sprintf("%d", delta.Size)})
				}

				// Add the image
				downloads[fingerprint] = imgDownloads
				images = append(images, image)

				return nil
			}

			// Locate a valid image
			for _, item := range version.Items {
				if item.FileType == "incus_combined.tar.gz" {
					err := addImage(&item, nil)
					if err != nil {
						continue
					}
				}

				if item.FileType == "incus.tar.xz" {
					// Locate the root files
					for _, subItem := range version.Items {
						if slices.Contains([]string{"disk1.img", "disk-kvm.img", "uefi1.img", "root.tar.xz", "squashfs"}, subItem.FileType) {
							err := addImage(&item, &subItem)
							if err != nil {
								continue
							}
						}
					}
				}
			}
		}
	}

	return images, downloads
}
