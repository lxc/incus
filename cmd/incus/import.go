package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/ioprogress"
	"github.com/lxc/incus/v7/shared/units"
)

type cmdImport struct {
	global *cmdGlobal

	flagStorage string
	flagConfig  []string
	flagDevice  []string
}

var cmdImportUsage = u.Usage{u.RemoteColonOpt, u.BackupFile, u.NewName(u.Instance).Optional()}

func (c *cmdImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("import", cmdImportUsage...)
	cmd.Short = i18n.G("Import instance backups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Import backups of instances including their snapshots.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus import backup0.tar.gz
    Create a new instance using backup0.tar.gz as the source.`))

	cmd.RunE = c.run
	cli.AddStringFlag(cmd.Flags(), &c.flagStorage, "storage|s", "", "", i18n.G("Storage pool name"))
	cli.AddStringArrayFlag(cmd.Flags(), &c.flagConfig, "config|c", i18n.G("Config key/value to apply to the new instance"))
	cli.AddStringArrayFlag(cmd.Flags(), &c.flagDevice, "device|d", i18n.G("New key/value to apply to a specific device"))

	return cmd
}

func (c *cmdImport) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdImportUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	backupFile := parsed[1].String
	instanceName := parsed[2].String

	var file *os.File
	if isStdin(backupFile) {
		file = os.Stdin
	} else {
		file, err = os.Open(backupFile)
		if err != nil {
			return err
		}

		defer func() { _ = file.Close() }()
	}

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	progress := cli.ProgressRenderer{
		Format: i18n.G("Importing instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	createArgs := incus.InstanceBackupArgs{
		BackupFile: &ioprogress.ProgressReader{
			ReadCloser: file,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		},
		PoolName: c.flagStorage,
		Name:     instanceName,
		Config:   c.flagConfig,
		Devices:  c.flagDevice,
	}

	op, err := d.CreateInstanceFromBackup(createArgs)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish.
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}
