package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/simplestreams"
)

type cmdAdd struct {
	global *cmdGlobal
}

// Command generates the command definition.
func (c *cmdAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "add <metadata tarball> <data file>"
	cmd.Short = "Add an image"
	cmd.Long = cli.FormatSection("Description",
		`Add an image to the server

This command parses the metadata tarball to retrieve the following fields from its metadata.yaml:
 - architecture
 - creation_date
 - properties["description"]
 - properties["os"]
 - properties["release"]
 - properties["variant"]
 - properties["architecture"]

It then check computes the hash for the new image, confirm it's not
already on the image server and finally adds it to the index.
`)
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Open the metadata.
	metaFile, err := os.Open(args[0])
	if err != nil {
		return err
	}

	defer metaFile.Close()

	// Read the header.
	_, _, unpacker, err := archive.DetectCompressionFile(metaFile)
	if err != nil {
		return err
	}

	// Get the size.
	metaStat, err := metaFile.Stat()
	if err != nil {
		return err
	}

	metaSize := metaStat.Size()

	// Get the sha256.
	_, err = metaFile.Seek(0, 0)
	if err != nil {
		return err
	}

	hash256 := sha256.New()
	_, err = io.Copy(hash256, metaFile)
	if err != nil {
		return err
	}

	metaSha256 := fmt.Sprintf("%x", hash256.Sum(nil))

	// Set the metadata paths.
	metaPath := args[0]
	metaTargetPath := fmt.Sprintf("images/%s.incus.tar.xz", metaSha256)

	// Go through the tarball.
	_, err = metaFile.Seek(0, 0)
	if err != nil {
		return err
	}

	metaTar, metaTarCancel, err := archive.CompressedTarReader(context.Background(), metaFile, unpacker, "")
	if err != nil {
		return err
	}

	defer metaTarCancel()

	var hdr *tar.Header
	for {
		hdr, err = metaTar.Next()
		if err != nil {
			if err == io.EOF {
				break
			}

			return err
		}

		if hdr.Name == "metadata.yaml" {
			break
		}
	}

	if hdr == nil || hdr.Name != "metadata.yaml" {
		return fmt.Errorf("Couldn't find metadata.yaml in metadata tarball")
	}

	// Parse the metadata.
	metadata := api.ImageMetadata{}

	body, err := io.ReadAll(metaTar)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(body, &metadata)
	if err != nil {
		return err
	}

	// Validate the metadata.
	_, err = osarch.ArchitectureId(metadata.Architecture)
	if err != nil {
		return fmt.Errorf("Invalid architecture in metadata.yaml: %w", err)
	}

	if metadata.CreationDate == 0 {
		return fmt.Errorf("Missing creation date in metadata.yaml")
	}

	for _, prop := range []string{"os", "release", "variant", "architecture", "description"} {
		_, ok := metadata.Properties[prop]
		if !ok {
			return fmt.Errorf("Missing property %q in metadata.yaml", prop)
		}
	}

	// Open the data.
	dataFile, err := os.Open(args[1])
	if err != nil {
		return err
	}

	defer dataFile.Close()

	// Read the header.
	_, dataExtension, _, err := archive.DetectCompressionFile(dataFile)
	if err != nil {
		return err
	}

	var dataItemType string
	if dataExtension == ".squashfs" {
		dataItemType = "squashfs"
	} else if dataExtension == ".qcow2" {
		dataItemType = "disk-kvm.img"
	} else {
		return fmt.Errorf("Unsupported data type %q", dataExtension)
	}

	// Get the size.
	dataStat, err := dataFile.Stat()
	if err != nil {
		return err
	}

	dataSize := dataStat.Size()

	// Get the sha256.
	_, err = dataFile.Seek(0, 0)
	if err != nil {
		return err
	}

	hash256 = sha256.New()
	_, err = io.Copy(hash256, dataFile)
	if err != nil {
		return err
	}

	dataSha256 := fmt.Sprintf("%x", hash256.Sum(nil))

	// Get the combined sha256.
	_, err = metaFile.Seek(0, 0)
	if err != nil {
		return err
	}

	_, err = dataFile.Seek(0, 0)
	if err != nil {
		return err
	}

	hash256 = sha256.New()
	_, err = io.Copy(hash256, metaFile)
	if err != nil {
		return err
	}

	_, err = io.Copy(hash256, dataFile)
	if err != nil {
		return err
	}

	combinedSha256 := fmt.Sprintf("%x", hash256.Sum(nil))

	// Set the data paths.
	dataPath := args[1]
	dataTargetPath := fmt.Sprintf("images/%s%s", metaSha256, dataExtension)

	// Create the paths if missing.
	err = os.MkdirAll("images", 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}

	err = os.MkdirAll("streams/v1", 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Load the images file.
	products := simplestreams.Products{}

	body, err = os.ReadFile("streams/v1/images.json")
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		// Create a blank images file.
		products = simplestreams.Products{
			ContentID: "images",
			DataType:  "image-downloads",
			Format:    "products:1.0",
			Products:  map[string]simplestreams.Product{},
		}
	} else {
		// Parse the existing images file.
		err = json.Unmarshal(body, &products)
		if err != nil {
			return err
		}
	}

	// Check if the product already exists.
	productName := fmt.Sprintf("%s:%s:%s:%s", metadata.Properties["os"], metadata.Properties["release"], metadata.Properties["variant"], metadata.Properties["architecture"])
	product, ok := products.Products[productName]
	if !ok {
		// Create a new product.
		product = simplestreams.Product{
			Aliases:         fmt.Sprintf("%s/%s/%s", metadata.Properties["os"], metadata.Properties["release"], metadata.Properties["variant"]),
			Architecture:    metadata.Properties["architecture"],
			OperatingSystem: metadata.Properties["os"],
			Release:         metadata.Properties["release"],
			ReleaseTitle:    metadata.Properties["release"],
			Variant:         metadata.Properties["variant"],
			Versions:        map[string]simplestreams.ProductVersion{},
		}
	}

	// Check if a version already exists.
	versionName := time.Unix(metadata.CreationDate, 0).Format("200601021504")
	version, ok := product.Versions[versionName]
	if !ok {
		// Create a new version.
		version = simplestreams.ProductVersion{
			Items: map[string]simplestreams.ProductVersionItem{
				"incus.tar.xz": {
					FileType:   "incus.tar.xz",
					HashSha256: metaSha256,
					Size:       metaSize,
					Path:       metaTargetPath,
				},
			},
		}
	} else {
		// Check that we're dealing with the same metadata.
		_, ok := version.Items["incus.tar.xz"]
		if !ok {
			// No incus.tar.xz found, add it.
			version.Items["incus.tar.xz"] = simplestreams.ProductVersionItem{
				FileType:   "incus.tar.xz",
				HashSha256: metaSha256,
				Size:       metaSize,
				Path:       metaTargetPath,
			}
		}
	}

	// Copy the metadata file if missing.
	err = internalUtil.FileCopy(metaPath, metaTargetPath)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Check that the data file isn't already in.
	_, ok = version.Items[dataItemType]
	if ok {
		return fmt.Errorf("Already have a %q file for this image", dataItemType)
	}

	// Add the file entry.
	version.Items[dataItemType] = simplestreams.ProductVersionItem{
		FileType:   dataItemType,
		HashSha256: dataSha256,
		Size:       dataSize,
		Path:       dataTargetPath,
	}

	// Add the combined hash.
	metaItem := version.Items["incus.tar.xz"]
	if dataItemType == "squashfs" {
		metaItem.CombinedSha256SquashFs = combinedSha256
	} else if dataItemType == "disk-kvm.img" {
		metaItem.CombinedSha256DiskKvmImg = combinedSha256
	}

	version.Items["incus.tar.xz"] = metaItem

	// Copy the data file if missing.
	err = internalUtil.FileCopy(dataPath, dataTargetPath)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Update the version.
	product.Versions[versionName] = version

	// Update the product.
	products.Products[productName] = product

	// Write back the images file.
	body, err = json.Marshal(&products)
	if err != nil {
		return err
	}

	err = os.WriteFile("streams/v1/images.json", body, 0644)
	if err != nil {
		return err
	}

	// Re-generate the index.
	err = writeIndex(&products)
	if err != nil {
		return err
	}

	return nil
}
