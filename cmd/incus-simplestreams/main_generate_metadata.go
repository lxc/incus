package main

import (
	"archive/tar"
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/ask"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/osarch"
)

type cmdGenerateMetadata struct {
	global *cmdGlobal

	flagOptions []string
}

func (c *cmdGenerateMetadata) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "generate-metadata <path>"
	cmd.Short = "Generate a metadata tarball"
	cmd.Long = cli.FormatSection("Description:",
		`Generate a metadata tarball

This command produces an incus.tar.xz tarball for use with an existing QCOW2 or squashfs disk image.

Fields given with --option are not prompted for. The fields that would
otherwise be asked are:
 - os (Operating system name)
 - release (Release name)
 - variant (Variant name)
 - architecture (Architecture name)
 - description (Description)

Any other --option key is written to the metadata as is, e.g.
 -o requirements.cdrom_agent=true
`)
	cmd.Flags().StringArrayVarP(&c.flagOptions, "option", "o", nil, "Set a metadata property (key=value, repeat as needed)")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGenerateMetadata) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := cli.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Setup asker.
	asker := ask.NewAsker(bufio.NewReader(os.Stdin))

	// Create the tarball.
	metaFile, err := os.Create(args[0])
	if err != nil {
		return err
	}

	defer logger.WarnOnError(metaFile.Close, "Failed to close file")

	// Collect the supplied properties.
	properties := map[string]string{}
	for _, option := range c.flagOptions {
		parts := strings.SplitN(option, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("Invalid option %q, expected key=value", option)
		}

		properties[parts[0]] = parts[1]
	}

	// Generate the metadata.
	timestamp := time.Now().UTC()
	metadata := api.ImageMetadata{
		Properties:   map[string]string{},
		CreationDate: timestamp.Unix(),
	}

	// Question - os
	metaOS, ok := properties["os"]
	if !ok {
		metaOS, err = asker.AskString("Operating system name: ", "", nil)
		if err != nil {
			return err
		}
	}

	metadata.Properties["os"] = metaOS

	// Question - release
	metaRelease, ok := properties["release"]
	if !ok {
		metaRelease, err = asker.AskString("Release name: ", "", nil)
		if err != nil {
			return err
		}
	}

	metadata.Properties["release"] = metaRelease

	// Question - variant
	metaVariant, ok := properties["variant"]
	if !ok {
		metaVariant, err = asker.AskString("Variant name [default=\"default\"]: ", "default", nil)
		if err != nil {
			return err
		}
	}

	metadata.Properties["variant"] = metaVariant

	// Question - architecture
	var incusArch string
	metaArchitecture, ok := properties["architecture"]
	if ok {
		id, err := osarch.ArchitectureID(metaArchitecture)
		if err != nil {
			return err
		}

		incusArch, err = osarch.ArchitectureName(id)
		if err != nil {
			return err
		}
	} else {
		metaArchitecture, err = asker.AskString("Architecture name: ", "", func(value string) error {
			id, err := osarch.ArchitectureID(value)
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
	}

	metadata.Properties["architecture"] = metaArchitecture
	metadata.Architecture = incusArch

	// Question - description
	defaultDescription := fmt.Sprintf("%s %s (%s) (%s) (%s)", metaOS, metaRelease, metaVariant, metaArchitecture, timestamp.Format("200601021504"))
	metaDescription, ok := properties["description"]
	if !ok {
		metaDescription, err = asker.AskString(fmt.Sprintf("Description [default=\"%s\"]: ", defaultDescription), defaultDescription, nil)
		if err != nil {
			return err
		}
	}

	metadata.Properties["description"] = metaDescription

	// Copy any other supplied property as is.
	for key, value := range properties {
		metadata.Properties[key] = value
	}

	// Generate YAML.
	body, err := yaml.Dump(&metadata, yaml.WithV2Defaults())
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
		Mode:    0o644,
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
