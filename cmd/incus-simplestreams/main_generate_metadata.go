package main

import (
	"archive/tar"
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/osarch"
)

type cmdGenerateMetadata struct {
	global *cmdGlobal
}

// Command generates the command definition.
func (c *cmdGenerateMetadata) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "generate-metadata <path>"
	cmd.Short = "Generate a metadata tarball"
	cmd.Long = cli.FormatSection("Description",
		`Generate a metadata tarball

This command produces an incus.tar.xz tarball for use with an existing QCOW2 or squashfs disk image.

This command will prompt for all of the metadata tarball fields:
 - Operating system name
 - Release
 - Variant
 - Architecture
 - Description
`)
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdGenerateMetadata) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Setup asker.
	asker := cli.NewAsker(bufio.NewReader(os.Stdin))

	// Create the tarball.
	metaFile, err := os.Create(args[0])
	if err != nil {
		return err
	}

	defer metaFile.Close()

	// Generate the metadata.
	timestamp := time.Now().UTC()
	metadata := api.ImageMetadata{
		Properties:   map[string]string{},
		CreationDate: timestamp.Unix(),
	}

	// Question - os
	metaOS, err := asker.AskString("Operating system name: ", "", nil)
	if err != nil {
		return err
	}

	metadata.Properties["os"] = metaOS

	// Question - release
	metaRelease, err := asker.AskString("Release name: ", "", nil)
	if err != nil {
		return err
	}

	metadata.Properties["release"] = metaRelease

	// Question - variant
	metaVariant, err := asker.AskString("Variant name [default=\"default\"]: ", "default", nil)
	if err != nil {
		return err
	}

	metadata.Properties["variant"] = metaVariant

	// Question - architecture
	var incusArch string
	metaArchitecture, err := asker.AskString("Architecture name: ", "", func(value string) error {
		id, err := osarch.ArchitectureId(value)
		if err != nil {
			return err
		}

		incusArch, err = osarch.ArchitectureName(id)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	metadata.Properties["architecture"] = metaArchitecture
	metadata.Architecture = incusArch

	// Question - description
	defaultDescription := fmt.Sprintf("%s %s (%s) (%s) (%s)", metaOS, metaRelease, metaVariant, metaArchitecture, timestamp.Format("200601021504"))
	metaDescription, err := asker.AskString(fmt.Sprintf("Description [default=\"%s\"]: ", defaultDescription), defaultDescription, nil)
	if err != nil {
		return err
	}

	metadata.Properties["description"] = metaDescription

	// Generate YAML.
	body, err := yaml.Marshal(&metadata)
	if err != nil {
		return err
	}

	// Prepare the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	tarWriter := tar.NewWriter(tarPipeWriter)

	// Compress the tarball.
	chDone := make(chan error)
	go func() {
		cmd := exec.Command("xz", "-9", "-c")
		cmd.Stdin = tarPipeReader
		cmd.Stdout = metaFile

		err := cmd.Run()
		chDone <- err
	}()

	// Add metadata.yaml.
	hdr := &tar.Header{
		Name:    "metadata.yaml",
		Size:    int64(len(body)),
		Mode:    0644,
		Uname:   "root",
		Gname:   "root",
		ModTime: time.Now(),
	}

	err = tarWriter.WriteHeader(hdr)
	if err != nil {
		return err
	}

	_, err = tarWriter.Write(body)
	if err != nil {
		return err
	}

	// Close the tarball.
	err = tarWriter.Close()
	if err != nil {
		return err
	}

	err = tarPipeWriter.Close()
	if err != nil {
		return err
	}

	err = <-chDone
	if err != nil {
		return err
	}

	return nil
}
