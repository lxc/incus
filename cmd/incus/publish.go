package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdPublish struct {
	global *cmdGlobal

	flagAliases              []string
	flagCompressionAlgorithm string
	flagExpiresAt            string
	flagMakePublic           bool
	flagForce                bool
	flagReuse                bool
	flagFormat               string
}

var cmdPublishUsage = u.Usage{u.MakePath(u.Instance, u.Snapshot.Optional()).Remote(), u.RemoteColonOpt, u.LegacyKV.List(0)}

func (c *cmdPublish) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("publish", cmdPublishUsage...)
	cmd.Short = i18n.G("Publish instances as images")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Publish instances as images`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagMakePublic, "public", false, i18n.G("Make the image public"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New alias to define at target")+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Stop the instance if currently running"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Compression algorithm to use (`none` for uncompressed)"))
	cmd.Flags().StringVar(&c.flagExpiresAt, "expire", "", i18n.G("Image expiration date (format: rfc3339)")+"``")
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the image alias already exists, delete and create a new one"))
	cmd.Flags().StringVar(&c.flagFormat, "format", "unified", i18n.G("Image format")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstancesAndSnapshots(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPublish) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdPublishUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	srcServer := parsed[0].RemoteServer
	isSnapshot := !parsed[0].RemoteObject.List[1].Skipped
	objectName := parsed[0].RemoteObject.String
	dstServer := parsed[1].RemoteServer
	keys, err := kvToMap(parsed[2])
	if err != nil {
		return err
	}

	if !isSnapshot {
		inst, etag, err := srcServer.GetInstance(objectName)
		if err != nil {
			return err
		}

		wasRunning := inst.StatusCode != 0 && inst.StatusCode != api.Stopped
		wasEphemeral := inst.Ephemeral

		if wasRunning {
			if !c.flagForce {
				return errors.New(i18n.G("The instance is currently running. Use --force to have it stopped and restarted"))
			}

			if inst.Ephemeral {
				// Clear the ephemeral flag so the instance can be stopped without being destroyed.
				inst.Ephemeral = false
				op, err := srcServer.UpdateInstance(objectName, inst.Writable(), etag)
				if err != nil {
					return err
				}

				err = op.Wait()
				if err != nil {
					return err
				}
			}

			// Stop the instance.
			req := api.InstanceStatePut{
				Action:  string(instance.Stop),
				Timeout: -1,
				Force:   true,
			}

			op, err := srcServer.UpdateInstanceState(objectName, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return errors.New(i18n.G("Stopping instance failed!"))
			}

			// Start the instance back up on exit.
			defer func() {
				req.Action = string(instance.Start)
				op, err = srcServer.UpdateInstanceState(objectName, req, "")
				if err != nil {
					return
				}

				_ = op.Wait()
			}()

			// If we had to clear the ephemeral flag, restore it now.
			if wasEphemeral {
				inst, etag, err := srcServer.GetInstance(objectName)
				if err != nil {
					return err
				}

				inst.Ephemeral = true
				op, err := srcServer.UpdateInstance(objectName, inst.Writable(), etag)
				if err != nil {
					return err
				}

				err = op.Wait()
				if err != nil {
					return err
				}
			}
		}
	}

	// Reformat aliases
	aliases := []api.ImageAlias{}
	for _, entry := range c.flagAliases {
		alias := api.ImageAlias{}
		alias.Name = entry
		aliases = append(aliases, alias)
	}

	// Create the image
	req := api.ImagesPost{
		Source: &api.ImagesPostSource{
			Type: "instance",
			Name: objectName,
		},
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	// We should only set the properties field if there actually are any.
	// Otherwise we will only delete any existing properties on publish.
	// This is something which only direct callers of the API are allowed to
	// do.
	if len(keys) > 0 {
		req.Properties = keys
	}

	if isSnapshot {
		req.Source.Type = "snapshot"
	} else if !srcServer.HasExtension("instances") {
		req.Source.Type = "container"
	}

	if srcServer == dstServer {
		req.Public = c.flagMakePublic
	}

	if c.flagExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, c.flagExpiresAt)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid expiration date: %w"), err)
		}

		req.ExpiresAt = expiresAt
	}

	existingAliases, err := getCommonAliases(dstServer, aliases...)
	if err != nil {
		return fmt.Errorf(i18n.G("Error retrieving aliases: %w"), err)
	}

	if !c.flagReuse && len(existingAliases) > 0 {
		names := []string{}
		for _, alias := range existingAliases {
			names = append(names, alias.Name)
		}

		return fmt.Errorf(i18n.G("Aliases already exists: %s"), strings.Join(names, ", "))
	}

	req.Format = c.flagFormat

	op, err := srcServer.CreateImage(req, nil)
	if err != nil {
		return err
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Publishing instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for the copy to complete
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	opAPI := op.Get()

	// Grab the fingerprint
	fingerprint, ok := opAPI.Metadata["fingerprint"].(string)
	if !ok {
		return errors.New("Bad fingerprint")
	}

	// For remote publish, copy to target now
	if srcServer != dstServer {
		defer func() { _, _ = srcServer.DeleteImage(fingerprint) }()

		// Get the source image
		image, _, err := srcServer.GetImage(fingerprint)
		if err != nil {
			return err
		}

		// Image copy arguments
		args := incus.ImageCopyArgs{
			Public: c.flagMakePublic,
		}

		// Copy the image to the destination host
		op, err := dstServer.CopyImage(srcServer, *image, &args)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	// Delete images if necessary
	if c.flagReuse {
		err = deleteImagesByAliases(dstServer, aliases)
		if err != nil {
			return err
		}
	}

	err = ensureImageAliases(dstServer, aliases, fingerprint)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Instance published with fingerprint: %s")+"\n", fingerprint)
	return nil
}
