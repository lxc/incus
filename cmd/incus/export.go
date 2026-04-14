package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdExport struct {
	global *cmdGlobal

	flagInstanceOnly         bool
	flagRootOnly             bool
	flagOptimizedStorage     bool
	flagCompressionAlgorithm string
	flagForce                bool
}

var cmdExportUsage = u.Usage{u.Instance.Remote(), u.Target(u.File).Optional()}

func (c *cmdExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("export", cmdExportUsage...)
	cmd.Short = i18n.G("Export instance backups")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Export instances as backup tarballs.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus export u1 backup0.tar.gz
	Download a backup tarball of the u1 instance.

incus export u1 -
	Download a backup tarball with it written to the standard output.`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false,
		i18n.G("Whether or not to only backup the instance (without snapshots)"))
	cmd.Flags().BoolVar(&c.flagRootOnly, "root-only", false,
		i18n.G("Whether or not to only backup the instance (without dependent volumes)"))
	cmd.Flags().BoolVar(&c.flagOptimizedStorage, "optimized-storage", false,
		i18n.G("Use storage driver optimized format (can only be restored on a similar pool)"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Compression algorithm to use (none for uncompressed)")+"``")
	cmd.Flags().BoolVar(&c.flagForce, "force", false, i18n.G("Force overwriting existing backup file"))

	return cmd
}

func (c *cmdExport) run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdExportUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	hasTarget := !parsed[1].Skipped
	targetName := parsed[1].Get("." + instanceName + ".backup")

	if isStdout(targetName) {
		// If outputting to stdout, quiesce the output.
		c.global.flagQuiet = true
	} else if hasTarget && !c.flagForce && util.PathExists(targetName) {
		// Check if the target path already exists.
		return fmt.Errorf(i18n.G("Target path %q already exists"), targetName)
	}

	instanceOnly := c.flagInstanceOnly

	req := api.InstanceBackupsPost{
		Name:                 "",
		ExpiresAt:            time.Now().Add(24 * time.Hour),
		InstanceOnly:         instanceOnly,
		RootOnly:             c.flagRootOnly,
		OptimizedStorage:     c.flagOptimizedStorage,
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	var getter func(backupReq *incus.BackupFileRequest) error

	if d.HasExtension("direct_backup") {
		getter = func(backupReq *incus.BackupFileRequest) error {
			return d.CreateInstanceBackupStream(instanceName, req, backupReq)
		}
	} else {
		// Send the request.
		op, err := d.CreateInstanceBackup(instanceName, req)
		if err != nil {
			return fmt.Errorf(i18n.G("Create instance backup: %w"), err)
		}

		// Watch the background operation.
		progress := cli.ProgressRenderer{
			Format: i18n.G("Backing up instance: %s"),
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait until backup is done.
		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")

		err = op.Wait()
		if err != nil {
			return err
		}

		// Get name of backup.
		uStr := op.Get().Resources["backups"][0]
		uri, err := url.Parse(uStr)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid URL %q: %w"), uStr, err)
		}

		backupName, err := url.PathUnescape(path.Base(uri.EscapedPath()))
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid backup name segment in path %q: %w"), uri.EscapedPath(), err)
		}

		defer func() {
			// Delete backup after we're done.
			op, err = d.DeleteInstanceBackup(instanceName, backupName)
			if err == nil {
				_ = op.Wait()
			}
		}()

		getter = func(backupReq *incus.BackupFileRequest) error {
			_, err := d.GetInstanceBackupFile(instanceName, backupName, backupReq)
			return err
		}
	}

	var target *os.File
	if isStdout(targetName) {
		target = os.Stdout
	} else {
		target, err = os.Create(targetName)
		if err != nil {
			return err
		}

		defer func() { _ = target.Close() }()
	}

	// Prepare the download request.
	progress := cli.ProgressRenderer{
		Format: i18n.G("Exporting the backup: %s"),
		Quiet:  c.global.flagQuiet,
	}

	backupFileRequest := incus.BackupFileRequest{
		BackupFile:      io.WriteSeeker(target),
		ProgressHandler: progress.UpdateProgress,
	}

	// Export tarball.
	err = getter(&backupFileRequest)
	if err != nil {
		_ = os.Remove(targetName)
		progress.Done("")
		return fmt.Errorf(i18n.G("Fetch instance backup file: %w"), err)
	}

	// Detect backup file type and rename file accordingly.
	if !hasTarget {
		_, err := target.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}

		_, ext, _, err := archive.DetectCompressionFile(target)
		if err != nil {
			return err
		}

		err = os.Rename(targetName, instanceName+ext)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to rename export file: %w"), err)
		}
	}

	err = target.Close()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to close export file: %w"), err)
	}

	progress.Done(i18n.G("Backup exported successfully!"))
	return nil
}
