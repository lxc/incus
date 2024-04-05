package main

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/shared/simplestreams"
)

type cmdRemove struct {
	global *cmdGlobal
}

// Command generates the command definition.
func (c *cmdRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "remove <fingerprint>"
	cmd.Short = "Remove an image"
	cmd.Long = cli.FormatSection("Description",
		`Remove an image from the server

This command locates the image from its fingerprint and removes it from the index.
`)
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Get a simplestreams client.
	ss := simplestreams.NewLocalClient("")

	// Get the image.
	image, err := ss.GetImage(args[0])
	if err != nil {
		return err
	}

	// Load the images file.
	body, err := os.ReadFile("streams/v1/images.json")
	if err != nil {
		return err
	}

	products := simplestreams.Products{}
	err = json.Unmarshal(body, &products)
	if err != nil {
		return err
	}

	// Delete the image entry.
	for kProduct, product := range products.Products {
		if product.OperatingSystem != image.Properties["os"] || product.Release != image.Properties["release"] || product.Variant != image.Properties["variant"] || product.Architecture != image.Properties["architecture"] {
			continue
		}

		for kVersion, version := range product.Versions {
			// Get the metadata entry.
			metaEntry, ok := version.Items["incus.tar.xz"]
			if !ok {
				// Image isn't using our normal structure.
				continue
			}

			if metaEntry.CombinedSha256DiskKvmImg == image.Fingerprint {
				// Deleting a VM image.
				err = os.Remove(version.Items["disk-kvm.img"].Path)
				if err != nil && !os.IsNotExist(err) {
					return err
				}

				delete(version.Items, "disk-kvm.img")
				metaEntry.CombinedSha256DiskKvmImg = ""
			} else if metaEntry.CombinedSha256SquashFs == image.Fingerprint {
				// Deleting a container image.
				err = os.Remove(version.Items["squashfs"].Path)
				if err != nil && !os.IsNotExist(err) {
					return err
				}

				delete(version.Items, "squashfs")
				metaEntry.CombinedSha256SquashFs = ""
			} else {
				continue
			}

			// Update the metadata entry.
			version.Items["incus.tar.xz"] = metaEntry

			// Delete the version if it's now empty.
			if len(version.Items) == 1 {
				err = os.Remove(metaEntry.Path)
				if err != nil && !os.IsNotExist(err) {
					return err
				}

				delete(product.Versions, kVersion)
			}

			break
		}

		if len(product.Versions) == 0 {
			delete(products.Products, kProduct)
		}

		break
	}

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
