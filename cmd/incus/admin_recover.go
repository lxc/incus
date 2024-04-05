//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/recover"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/validate"
)

type cmdAdminRecover struct {
	global *cmdGlobal
}

func (c *cmdAdminRecover) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("recover")
	cmd.Short = i18n.G("Recover missing instances and volumes from existing and unknown storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Recover missing instances and volumes from existing and unknown storage pools

  This command is mostly used for disaster recovery. It will ask you about unknown storage pools and attempt to
  access them, along with existing storage pools, and identify any missing instances and volumes that exist on the
  pools but are not in the database. It will then offer to recreate these database records.`))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAdminRecover) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) > 0 {
		return fmt.Errorf(i18n.G("Invalid arguments"))
	}

	d, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return err
	}

	server, _, err := d.GetServer()
	if err != nil {
		return err
	}

	isClustered := d.IsClustered()

	// Get list of existing storage pools to scan.
	existingPools, err := d.GetStoragePools()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed getting existing storage pools: %w"), err)
	}

	fmt.Println(i18n.G("This server currently has the following storage pools:"))
	for _, existingPool := range existingPools {
		fmt.Printf(" - "+i18n.G("%s (backend=%q, source=%q)")+"\n", existingPool.Name, existingPool.Driver, existingPool.Config["source"])
	}

	unknownPools := make([]api.StoragePoolsPost, 0, len(existingPools))

	// Build up a list of unknown pools to scan.
	// We don't offer this option if the server is clustered because we don't allow creating storage pools on
	// an individual server when clustered.
	if !isClustered {
		var supportedDriverNames []string

		for {
			addUnknownPool, err := c.global.asker.AskBool(i18n.G("Would you like to recover another storage pool?")+" (yes/no) [default=no]: ", "no")
			if err != nil {
				return err
			}

			if !addUnknownPool {
				break
			}

			// Get available storage drivers if not done already.
			if supportedDriverNames == nil {
				for _, supportedDriver := range server.Environment.StorageSupportedDrivers {
					supportedDriverNames = append(supportedDriverNames, supportedDriver.Name)
				}
			}

			unknownPool := api.StoragePoolsPost{
				StoragePoolPut: api.StoragePoolPut{
					Config: make(map[string]string),
				},
			}

			unknownPool.Name, err = c.global.asker.AskString(i18n.G("Name of the storage pool:")+" ", "", validate.Required(func(value string) error {
				if value == "" {
					return fmt.Errorf(i18n.G("Pool name cannot be empty"))
				}

				for _, p := range unknownPools {
					if value == p.Name {
						return fmt.Errorf(i18n.G("Storage pool %q is already on recover list"), value)
					}
				}

				return nil
			}))
			if err != nil {
				return err
			}

			unknownPool.Driver, err = c.global.asker.AskString(fmt.Sprintf(i18n.G("Name of the storage backend (%s):")+" ", strings.Join(supportedDriverNames, ", ")), "", validate.IsOneOf(supportedDriverNames...))
			if err != nil {
				return err
			}

			unknownPool.Config["source"], err = c.global.asker.AskString(i18n.G("Source of the storage pool (block device, volume group, dataset, path, ... as applicable):")+" ", "", validate.IsNotEmpty)
			if err != nil {
				return err
			}

			for {
				var configKey, configValue string
				_, _ = c.global.asker.AskString(i18n.G("Additional storage pool configuration property (KEY=VALUE, empty when done):")+" ", "", validate.Optional(func(value string) error {
					configParts := strings.SplitN(value, "=", 2)
					if len(configParts) < 2 {
						return fmt.Errorf(i18n.G("Config option should be in the format KEY=VALUE"))
					}

					configKey = configParts[0]
					configValue = configParts[1]

					return nil
				}))

				if configKey == "" {
					break
				}

				unknownPool.Config[configKey] = configValue
			}

			unknownPools = append(unknownPools, unknownPool)
		}
	}

	fmt.Println(i18n.G("The recovery process will be scanning the following storage pools:"))
	for _, p := range existingPools {
		fmt.Printf(" - "+i18n.G("EXISTING: %q (backend=%q, source=%q)")+"\n", p.Name, p.Driver, p.Config["source"])
	}

	for _, p := range unknownPools {
		fmt.Printf(" - "+i18n.G("NEW: %q (backend=%q, source=%q)")+"\n", p.Name, p.Driver, p.Config["source"])
	}

	proceed, err := c.global.asker.AskBool(i18n.G("Would you like to continue with scanning for lost volumes?")+" (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !proceed {
		return nil
	}

	fmt.Println(i18n.G("Scanning for unknown volumes..."))

	// Send /internal/recover/validate request to the daemon.
	reqValidate := recover.ValidatePost{
		Pools: make([]api.StoragePoolsPost, 0, len(existingPools)+len(unknownPools)),
	}

	// Add existing pools to request.
	for _, p := range existingPools {
		reqValidate.Pools = append(reqValidate.Pools, api.StoragePoolsPost{
			Name: p.Name, // Only send existing pool name, the rest will be looked up on server.
		})
	}

	// Add unknown pools to request.
	reqValidate.Pools = append(reqValidate.Pools, unknownPools...)

	for {
		resp, _, err := d.RawQuery("POST", "/internal/recover/validate", reqValidate, "")
		if err != nil {
			return fmt.Errorf(i18n.G("Failed validation request: %w"), err)
		}

		var res recover.ValidateResult

		err = resp.MetadataAsStruct(&res)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed parsing validation response: %w"), err)
		}

		if len(unknownPools) > 0 {
			fmt.Println(i18n.G("The following unknown storage pools have been found:"))
			for _, unknownPool := range unknownPools {
				fmt.Printf(" - "+i18n.G("Storage pool %q of type %q")+"\n", unknownPool.Name, unknownPool.Driver)
			}
		}

		if len(res.UnknownVolumes) > 0 {
			fmt.Println(i18n.G("The following unknown volumes have been found:"))
			for _, unknownVol := range res.UnknownVolumes {
				fmt.Printf(" - "+i18n.G("%s %q on pool %q in project %q (includes %d snapshots)")+"\n", cases.Title(language.English).String(unknownVol.Type), unknownVol.Name, unknownVol.Pool, unknownVol.Project, unknownVol.SnapshotCount)
			}
		}

		if len(res.DependencyErrors) > 0 {
			fmt.Println(i18n.G("You are currently missing the following:"))
			for _, depErr := range res.DependencyErrors {
				fmt.Printf(" - %s\n", depErr)
			}

			_, _ = c.global.asker.AskString(i18n.G("Please create those missing entries and then hit ENTER:")+" ", "", validate.Optional())
		} else {
			if len(unknownPools) == 0 && len(res.UnknownVolumes) == 0 {
				fmt.Println(i18n.G("No unknown storage pools or volumes found. Nothing to do."))
				return nil
			}

			break // Dependencies met.
		}
	}

	proceed, err = c.global.asker.AskBool(i18n.G("Would you like those to be recovered?")+" (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if !proceed {
		return nil
	}

	fmt.Println(i18n.G("Starting recovery..."))

	// Send /internal/recover/import request to the daemon.
	// Don't lint next line with gosimple. It says we should convert reqValidate directly to an RecoverImportPost
	// because their types are identical. This is less clear and will not work if either type changes in the future.
	reqImport := recover.ImportPost{ //nolint:gosimple
		Pools: reqValidate.Pools,
	}

	_, _, err = d.RawQuery("POST", "/internal/recover/import", reqImport, "")
	if err != nil {
		return fmt.Errorf(i18n.G("Failed import request: %w"), err)
	}

	return nil
}
