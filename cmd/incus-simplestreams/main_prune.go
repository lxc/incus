package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/shared/simplestreams"
)

type cmdPrune struct {
	global *cmdGlobal

	flagDryRun    bool
	flagRetention int
	flagVerbose   bool
}

// Command generates the command definition.
func (c *cmdPrune) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "prune"
	cmd.Short = "Clean up obsolete files and data"
	cmd.Long = cli.FormatSection("Description",
		`Cleans up obsolete tarball files and removes outdated versions of a product

The prune command scans the project directory for tarball files that do not have corresponding references
in the 'images.json' file. Any tarball file that is not listed in images.json is considered orphaned
and will be deleted.
Additionally this command will delete older images, keeping a configurable number of older images per product.`)

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagDryRun, "dry-run", "d", false, "Preview changes without executing actual operations")
	cmd.Flags().IntVarP(&c.flagRetention, "retention", "r", 2, "Number of older versions of the product to preserve"+"``")
	cmd.Flags().BoolVarP(&c.flagVerbose, "verbose", "v", false, "Show all information messages")

	return cmd
}

// Run runs the actual command logic.
func (c *cmdPrune) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	if c.flagDryRun {
		c.flagVerbose = true
	}

	err = c.prune()
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdPrune) pruneFiles(products *simplestreams.Products, filesToPreserve []string) error {
	deletedFiles := []string{}
	err := filepath.WalkDir("./images", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Omit the path if it is a directory or if it exists in the images.json file.
		if d.IsDir() || slices.Contains(filesToPreserve, path) {
			return nil
		}

		if c.flagVerbose {
			deletedFiles = append(deletedFiles, path)
		}

		if !c.flagDryRun {
			e := os.Remove(path)
			if e != nil {
				return e
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if c.flagVerbose && len(deletedFiles) > 0 {
		fmt.Print("Following files were removed:\n")
		for _, file := range deletedFiles {
			fmt.Println(file)
		}
	}

	return nil
}

func (c *cmdPrune) prune() error {
	body, err := os.ReadFile("streams/v1/images.json")
	if err != nil {
		return err
	}

	products := simplestreams.Products{}
	err = json.Unmarshal(body, &products)
	if err != nil {
		return err
	}

	filesToPreserve := []string{}
	deletedItems := []string{}
	deletedVersions := []string{}
	for kProduct, product := range products.Products {
		versionNames := []string{}
		for kVersion, version := range product.Versions {
			for kItem, item := range version.Items {
				_, err := os.Stat(item.Path)
				if err != nil {
					if !errors.Is(err, os.ErrNotExist) {
						return err
					}

					if c.flagVerbose {
						deletedItems = append(deletedItems, fmt.Sprintf("%s:%s:%s", kProduct, kVersion, item.Path))
					}

					// Corresponding file doesn't exist on disk. Remove item from products.
					delete(version.Items, kItem)
				}

				filesToPreserve = append(filesToPreserve, item.Path)
			}

			if len(version.Items) == 0 {
				delete(product.Versions, kVersion)
				continue
			}

			versionNames = append(versionNames, kVersion)
		}

		if len(product.Versions) == 0 {
			delete(products.Products, kProduct)
			continue
		}

		sort.Strings(versionNames)

		updatedVersions := map[string]simplestreams.ProductVersion{}
		iteration := 0
		for i := len(versionNames) - 1; i >= 0; i-- {
			version := versionNames[i]
			if iteration <= c.flagRetention {
				updatedVersions[version] = product.Versions[version]
			} else if c.flagVerbose {
				deletedVersions = append(deletedVersions, fmt.Sprintf("%s:%s", kProduct, version))
			}

			iteration += 1
		}

		p := products.Products[kProduct]
		p.Versions = updatedVersions
		products.Products[kProduct] = p
	}

	if c.flagVerbose {
		if len(deletedItems) > 0 {
			fmt.Print("Following items were removed from images.json:\n")
			for _, item := range deletedItems {
				fmt.Println(item)
			}
		}

		if len(deletedVersions) > 0 {
			fmt.Print("Following versions were removed:\n")
			for _, version := range deletedVersions {
				fmt.Println(version)
			}
		}
	}

	if !c.flagDryRun {
		// Write back the images file.
		body, err = json.Marshal(&products)
		if err != nil {
			return err
		}

		err = os.WriteFile("streams/v1/images.json", body, 0o644)
		if err != nil {
			return err
		}

		// Re-generate the index.
		err = writeIndex(&products)
		if err != nil {
			return err
		}
	}

	err = c.pruneFiles(&products, filesToPreserve)
	if err != nil {
		return err
	}

	return nil
}
