package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/shared/simplestreams"
)

type cmdVerify struct {
	global *cmdGlobal
}

// Command generates the command definition.
func (c *cmdVerify) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "verify"
	cmd.Short = "Verify the integrity of the server"
	cmd.Long = cli.FormatSection("Description",
		`Verify the integrity of the server

This command will analyze the image index and for every image and file
in the index, will validate that the files on disk exist and are of the
correct size and content.
`)
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdVerify) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	// Load the images file.
	products := simplestreams.Products{}

	body, err := os.ReadFile("streams/v1/images.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	// Parse the existing images file.
	err = json.Unmarshal(body, &products)
	if err != nil {
		return err
	}

	// Go over all the files.
	for _, product := range products.Products {
		for _, version := range product.Versions {
			for _, item := range version.Items {
				// Open the data.
				dataFile, err := os.Open(item.Path)
				if err != nil {
					if os.IsNotExist(err) {
						return fmt.Errorf("Missing image file %q", item.Path)
					}

					return err
				}

				// Get the size.
				dataStat, err := dataFile.Stat()
				if err != nil {
					return err
				}

				if item.Size != dataStat.Size() {
					return fmt.Errorf("File %q has a different size than listed in the index", item.Path)
				}

				// Get the sha256.
				_, err = dataFile.Seek(0, 0)
				if err != nil {
					return err
				}

				hash256 := sha256.New()
				_, err = io.Copy(hash256, dataFile)
				if err != nil {
					return err
				}

				dataSha256 := fmt.Sprintf("%x", hash256.Sum(nil))
				if item.HashSha256 != dataSha256 {
					return fmt.Errorf("File %q has a different SHA256 hash than listed in the index", item.Path)
				}

				// Done with this file.
				dataFile.Close()
			}
		}
	}

	return nil
}
