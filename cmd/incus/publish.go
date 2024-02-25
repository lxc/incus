package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/shared/api"
)

type cmdPublish struct {
	global *cmdGlobal

	flagAliases              []string
	flagCompressionAlgorithm string
	flagExpiresAt            string
	flagMakePublic           bool
	flagForce                bool
	flagReuse                bool
}

func (c *cmdPublish) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("publish", i18n.G("[<remote>:]<instance>[/<snapshot>] [<remote>:] [flags] [key=value...]"))
	cmd.Short = i18n.G("Publish instances as images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Publish instances as images`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagMakePublic, "public", false, i18n.G("Make the image public"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New alias to define at target")+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Stop the instance if currently running"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Compression algorithm to use (`none` for uncompressed)"))
	cmd.Flags().StringVar(&c.flagExpiresAt, "expire", "", i18n.G("Image expiration date (format: rfc3339)")+"``")
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, i18n.G("If the image alias already exists, delete and create a new one"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPublish) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	iName := ""
	iRemote := ""
	properties := map[string]string{}
	firstprop := 1 // first property is arg[2] if arg[1] is image remote, else arg[1]

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	cRemote, cName, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	if len(args) >= 2 && !strings.Contains(args[1], "=") {
		firstprop = 2
		iRemote, iName, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	} else {
		iRemote, iName, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	if cName == "" {
		return fmt.Errorf(i18n.G("Instance name is mandatory"))
	}

	if iName != "" {
		return fmt.Errorf(i18n.G("There is no \"image name\".  Did you want an alias?"))
	}

	d, err := conf.GetInstanceServer(iRemote)
	if err != nil {
		return err
	}

	s := d
	if cRemote != iRemote {
		s, err = conf.GetInstanceServer(cRemote)
		if err != nil {
			return err
		}
	}

	if !instance.IsSnapshot(cName) {
		ct, etag, err := s.GetInstance(cName)
		if err != nil {
			return err
		}

		wasRunning := ct.StatusCode != 0 && ct.StatusCode != api.Stopped
		wasEphemeral := ct.Ephemeral

		if wasRunning {
			if !c.flagForce {
				return fmt.Errorf(i18n.G("The instance is currently running. Use --force to have it stopped and restarted"))
			}

			if ct.Ephemeral {
				// Clear the ephemeral flag so the instance can be stopped without being destroyed.
				ct.Ephemeral = false
				op, err := s.UpdateInstance(cName, ct.Writable(), etag)
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

			op, err := s.UpdateInstanceState(cName, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf(i18n.G("Stopping instance failed!"))
			}

			// Start the instance back up on exit.
			defer func() {
				req.Action = string(instance.Start)
				op, err = s.UpdateInstanceState(cName, req, "")
				if err != nil {
					return
				}

				_ = op.Wait()
			}()

			// If we had to clear the ephemeral flag, restore it now.
			if wasEphemeral {
				ct, etag, err := s.GetInstance(cName)
				if err != nil {
					return err
				}

				ct.Ephemeral = true
				op, err := s.UpdateInstance(cName, ct.Writable(), etag)
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

	for i := firstprop; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
		}

		properties[entry[0]] = entry[1]
	}

	// We should only set the properties field if there actually are any.
	// Otherwise we will only delete any existing properties on publish.
	// This is something which only direct callers of the API are allowed to
	// do.
	if len(properties) == 0 {
		properties = nil
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
			Name: cName,
		},
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	req.Properties = properties

	if instance.IsSnapshot(cName) {
		req.Source.Type = "snapshot"
	} else if !s.HasExtension("instances") {
		req.Source.Type = "container"
	}

	if cRemote == iRemote {
		req.Public = c.flagMakePublic
	}

	if c.flagExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, c.flagExpiresAt)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid expiration date: %w"), err)
		}

		req.ExpiresAt = expiresAt
	}

	existingAliases, err := GetCommonAliases(d, aliases...)
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

	op, err := s.CreateImage(req, nil)
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
	fingerprint := opAPI.Metadata["fingerprint"].(string)

	// For remote publish, copy to target now
	if cRemote != iRemote {
		defer func() { _, _ = s.DeleteImage(fingerprint) }()

		// Get the source image
		image, _, err := s.GetImage(fingerprint)
		if err != nil {
			return err
		}

		// Image copy arguments
		args := incus.ImageCopyArgs{
			Public: c.flagMakePublic,
		}

		// Copy the image to the destination host
		op, err := d.CopyImage(s, *image, &args)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	// Delete images if necessary
	if c.flagReuse && len(existingAliases) > 0 {
		visitedImages := make(map[string]interface{})
		for _, alias := range existingAliases {
			image, _, _ := d.GetImage(alias.Target)

			// If the image has already been visited then continue
			if image != nil {
				_, found := visitedImages[image.Fingerprint]
				if found {
					continue
				}

				visitedImages[image.Fingerprint] = nil
			}

			// An image can have multiple aliases. If an image being published
			// reuses all the aliases from an existing image then that existing image is removed.
			// In other case only specific aliases should be removed. E.g.
			// 1. If image with 'foo' and 'bar' aliases already exists and new image is published
			//    with aliases 'foo' and 'bar' (and flag '--reuse'). Old image should be removed.
			// 2. If image with 'foo' and 'bar' aliases already exists and new image is published
			//    with alias 'foo' (and flag '--reuse'). Old image should be kept with alias 'bar'
			//    and new image will have 'foo' alias.
			if image != nil && IsAliasesSubset(image.Aliases, aliases) {
				op, err := d.DeleteImage(alias.Target)
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

	err = ensureImageAliases(d, aliases, fingerprint)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Instance published with fingerprint: %s")+"\n", fingerprint)
	return nil
}
