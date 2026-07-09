package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	internalIO "github.com/lxc/incus/v7/internal/io"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/ioprogress"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/termios"
	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdStorageVolumeFile struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagUID  int
	flagGID  int
	flagMode string

	flagMkdir bool
}

func (c *cmdStorageVolumeFile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("file")
	cmd.Short = i18n.G("Manage files in custom volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage files in custom volumes`))

	// Create
	storageVolumeFileCreateCmd := cmdStorageVolumeFileCreate{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFileCreateCmd.command())

	// Delete
	storageVolumeFileDeleteCmd := cmdStorageVolumeFileDelete{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFileDeleteCmd.command())

	// Mount
	storageVolumeFileMountCmd := cmdStorageVolumeFileMount{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c}
	cmd.AddCommand(storageVolumeFileMountCmd.command())

	// Pull
	storageVolumeFilePullCmd := cmdStorageVolumeFilePull{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c, puller: &pullable{}}
	cmd.AddCommand(storageVolumeFilePullCmd.command())

	// Push
	storageVolumeFilePushCmd := cmdStorageVolumeFilePush{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeFile: c, pusher: &pushable{}}
	cmd.AddCommand(storageVolumeFilePushCmd.command())

	// Edit
	storageVolumeFileEditCmd := cmdStorageVolumeFileEdit{global: c.global, filePull: &storageVolumeFilePullCmd, filePush: &storageVolumeFilePushCmd}
	cmd.AddCommand(storageVolumeFileEditCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageVolumeFileCreate struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	flagForce bool
	flagType  string
}

var cmdStorageVolumeFileCreateUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path), u.SymlinkTargetPath.Optional()}

func (c *cmdStorageVolumeFileCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdStorageVolumeFileCreateUsage...)
	cmd.Short = i18n.G("Create files and directories in custom vollume")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create files and directories in custom volume`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume file create foo bar/baz
   To create a file baz in the bar volume on the foo pool.

incus file create --type=symlink foo bar/baz qux
   To create a symlink qux in bar storage volume on the foo pool whose target is baz.`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.storageVolumeFile.flagMkdir, "create-dirs|p", i18n.G("Create any directories necessary"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagForce, "force|f", i18n.G("Force creating files or directories"))
	cli.AddIntFlag(cmd.Flags(), &c.storageVolumeFile.flagGID, "gid", -1, i18n.G("Set the file's gid on create"))
	cli.AddIntFlag(cmd.Flags(), &c.storageVolumeFile.flagUID, "uid", -1, i18n.G("Set the file's uid on create"))
	cli.AddStringFlag(cmd.Flags(), &c.storageVolumeFile.flagMode, "mode", "", "", i18n.G("Set the file's perms on create"))
	cli.AddStringFlag(cmd.Flags(), &c.flagType, "type|t", "file", "", i18n.G("The type to create (file, symlink, or directory)"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeFileCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeFileCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[0].String
	targetPath, isDir := normalizePath(parsed[1].List[1].String)
	isSymlink := !parsed[2].Skipped
	symlinkTargetPath := path.Clean(parsed[2].String)

	if !slices.Contains([]string{"file", "symlink", "directory"}, c.flagType) {
		return fmt.Errorf(i18n.G("Invalid type %q"), c.flagType)
	}

	if isSymlink && c.flagType != "symlink" {
		return errors.New(i18n.G(`Symlink target path can only be used for type "symlink"`))
	}

	if isDir {
		c.flagType = "directory"
	}

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return err
	}

	defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

	// Determine the target uid
	uid := max(c.storageVolumeFile.flagUID, 0)

	// Determine the target gid
	gid := max(c.storageVolumeFile.flagGID, 0)

	var mode os.FileMode

	// Determine the target mode
	switch c.flagType {
	case "directory":
		mode = os.FileMode(DirMode)
	case "file":
		mode = os.FileMode(FileMode)
	}

	if c.storageVolumeFile.flagMode != "" {
		if len(c.storageVolumeFile.flagMode) == 3 {
			c.storageVolumeFile.flagMode = "0" + c.storageVolumeFile.flagMode
		}

		m, err := strconv.ParseInt(c.storageVolumeFile.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = os.FileMode(m)
	}

	// Create needed paths if requested
	if c.storageVolumeFile.flagMkdir {
		err := sftpRecursiveMkdir(sftpConn, path.Dir(targetPath), nil, int64(uid), int64(gid))
		if err != nil {
			return err
		}
	}

	var content io.ReadSeeker
	var readCloser io.ReadCloser
	var contentLength int64

	switch c.flagType {
	case "symlink":
		content = strings.NewReader(symlinkTargetPath)
		readCloser = io.NopCloser(content)
		contentLength = int64(len(symlinkTargetPath))
	case "file":
		// Just creating an empty file.
		content = strings.NewReader("")
		readCloser = io.NopCloser(content)
		contentLength = 0
	}

	fileArgs := incus.InstanceFileArgs{
		Type:    c.flagType,
		UID:     int64(uid),
		GID:     int64(gid),
		Mode:    int(mode.Perm()),
		Content: content,
	}

	if c.flagForce {
		fileArgs.WriteMode = "overwrite"
	}

	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Creating %s: %%s"), targetPath),
		Quiet:  c.global.flagQuiet,
	}

	if readCloser != nil {
		fileArgs.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: readCloser,
			Tracker: &ioprogress.ProgressTracker{
				Length: contentLength,
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, fileArgs.Content)
	}

	err = sftpCreateFile(sftpConn, targetPath, fileArgs, true)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

// Delete.
type cmdStorageVolumeFileDelete struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	flagForce bool
}

var cmdStorageVolumeFileDeleteUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path)}

func (c *cmdStorageVolumeFileDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdStorageVolumeFileDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete files in custom volume")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete files in custom volume`))

	cli.AddBoolFlag(cmd.Flags(), &c.flagForce, "force|f", i18n.G("Force deleting files, directories, and subdirectories"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpFiles(toComplete, false)
	}

	return cmd
}

func (c *cmdStorageVolumeFileDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeFileDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].List[0].String
	fPath := parsed[1].List[1].String

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return err
	}

	defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

	if c.flagForce {
		err = sftpConn.RemoveAll(fPath)
		if err != nil {
			return err
		}

		return nil
	}

	err = sftpConn.Remove(fPath)
	if err != nil {
		return err
	}

	return nil
}

// Mount.
type cmdStorageVolumeFileMount struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile

	flagListen   string
	flagAuthNone bool
	flagAuthUser string
}

var cmdStorageVolumeFileMountUsage = u.Usage{u.Pool.Remote(), u.Volume, u.Target(u.Path).Optional()}

func (c *cmdStorageVolumeFileMount) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("mount", cmdStorageVolumeFileMountUsage...)
	cmd.Short = i18n.G("Mount files from custom storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Mount files from custom storage volumes.
If no target path is provided, start an SSH SFTP listener instead.`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(`incus storage volume file mount mypool myvolume localdir
   To mount the storage volume myvolume from pool mypool onto the local directory localdir.

incus storage volume file mount mypool myvolume
   To start an SSH SFTP listener for the storage volume myvolume from pool mypool.`))

	cli.AddStringFlag(cmd.Flags(), &c.flagListen, "listen", "", "", i18n.G("Setup SSH SFTP listener on address:port instead of mounting"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAuthNone, "no-auth", i18n.G("Disable authentication when using SSH SFTP listener"))
	cli.AddStringFlag(cmd.Flags(), &c.flagAuthUser, "auth-user", "", "", i18n.G("Set authentication user when using SSH SFTP listener"))

	cmd.RunE = c.run

	// completion for pool, volume, host path
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		if len(args) == 2 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeFileMount) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeFileMountUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	poolName := parsed[0].RemoteObject.String
	volName := parsed[1].String
	hasTargetPath := !parsed[2].Skipped
	targetPath := filepath.Clean(parsed[2].String)
	entity := poolName + "/custom/" + volName

	// Determine the target if specified.
	if hasTargetPath {
		sb, err := os.Stat(targetPath)
		if err != nil {
			return err
		}

		if !sb.IsDir() {
			return errors.New(i18n.G("Target path must be a directory"))
		}

		// Check which mode we should operate in. If target path is provided we use sshfs mode.
		if c.flagListen != "" {
			return errors.New(i18n.G("Target path and --listen flag cannot be used together"))
		}

		// Connect to SFTP.
		sftpConn, err := d.GetStoragePoolVolumeFileSFTPConn(poolName, "custom", volName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
		}

		defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

		return sshfsMount(cmd.Context(), sftpConn, entity, "", targetPath)
	}

	// Check if the pool and the volume exist before starting the SFTP server.
	_, _, err = d.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return err
	}

	return sshSFTPServer(cmd.Context(), func() (net.Conn, error) {
		return d.GetStoragePoolVolumeFileSFTPConn(poolName, "custom", volName)
	}, c.flagAuthNone, c.flagAuthUser, c.flagListen)
}

// Edit.
type cmdStorageVolumeFileEdit struct {
	global   *cmdGlobal
	filePull *cmdStorageVolumeFilePull
	filePush *cmdStorageVolumeFilePush
}

var cmdStorageVolumeFileEditUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path)}

func (c *cmdStorageVolumeFileEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdStorageVolumeFileEditUsage...)
	cmd.Short = i18n.G("Edit files in storage volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit files in storage volumes`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeFileEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeFileEditUsage, cmd, args)
	if err != nil {
		return err
	}

	fPath := parsed[1].List[1].String

	c.filePush.noModeChange = true

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		return c.filePush.push(os.Stdin.Name(), parsed[0], parsed[1])
	}

	// Create temp file
	f, err := os.CreateTemp("", fmt.Sprintf("incus_file_edit_*%s", path.Ext(fPath)))
	if err != nil {
		return fmt.Errorf(i18n.G("Unable to create a temporary file: %v"), err)
	}

	fname := f.Name()
	_ = f.Close()
	_ = os.Remove(fname)

	// Tell pull/push that they're called from edit.
	c.filePull.edit = true
	c.filePush.edit = true

	// Extract current value
	defer logger.WarnOnError(func() error { return os.Remove(fname) }, "Failed to remove temporary file")
	err = c.filePull.pull(parsed[0], parsed[1], fname)
	if err != nil {
		return err
	}

	// Spawn the editor
	_, err = cli.TextEditor(fname, []byte{})
	if err != nil {
		return err
	}

	// Push the result
	err = c.filePush.push(fname, parsed[0], parsed[1])
	if err != nil {
		return err
	}

	return nil
}

// Pull.
type cmdStorageVolumeFilePull struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile
	puller            *pullable

	edit bool
}

var cmdStorageVolumeFilePullUsage = u.Usage{u.Pool.Remote(), u.MakePath(u.Volume, u.Path), u.Target(u.Path)}

func (c *cmdStorageVolumeFilePull) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("pull", cmdStorageVolumeFilePullUsage...)
	cmd.Short = i18n.G("Pull files from custom volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Pull files from custom volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus custom volume file pull local v1/foo/etc/hosts .
   To pull /etc/hosts from the custom volume and write it to the current directory.

incus file pull local v1 foo/etc/hosts -
   To pull /etc/hosts from the custom volume and write its output to standard output.`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.storageVolumeFile.flagMkdir, "create-dirs|p", i18n.G("Create any directories necessary"))
	cli.AddBoolFlag(cmd.Flags(), &c.puller.flagRecursive, "recursive|r", i18n.G("Recursively transfer files"))
	cli.AddBoolFlag(cmd.Flags(), &c.puller.flagNoDereference, "no-dereference|P", i18n.G("Never follow symbolic links in source path"))
	cli.AddBoolFlag(cmd.Flags(), &c.puller.flagFollow, "follow|H", i18n.G("Follow command-line symbolic links in source path"))
	cli.AddBoolFlag(cmd.Flags(), &c.puller.flagDereference, "dereference|L", i18n.G("Always follow symbolic links in source path"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return c.global.cmpFiles(toComplete, true)
	}

	return cmd
}

// pull runs the post-parsing command logic.
func (c *cmdStorageVolumeFilePull) pull(parsedPool *u.Parsed, parsedPath *u.Parsed, target string) error {
	d := parsedPool.RemoteServer
	poolName := parsedPool.RemoteObject.String
	volName := parsedPath.List[0].String
	fPath := "/" + parsedPath.List[1].String

	targetIsDir := strings.HasSuffix(target, "/")
	targetExists := true
	targetInfo, err := os.Stat(target)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		targetExists = false
	}

	err = c.puller.preCheck(target)
	if err != nil {
		return err
	}

	/*
	 * If the path exists, just use it. If it doesn't exist, it might be a
	 * directory in one of two cases:
	 *   1. Someone explicitly put "/" at the end
	 *   2. We are dealing with recursive copy
	 */
	if targetExists {
		targetIsDir = targetInfo.IsDir()
	} else if targetIsDir {
		err := os.MkdirAll(target, DirMode)
		if err != nil {
			return err
		}
	} else if c.storageVolumeFile.flagMkdir {
		err := os.MkdirAll(filepath.Dir(target), DirMode)
		if err != nil {
			return err
		}
	}

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
	}

	defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

	srcInfo, normalizedPath, err := c.puller.statFile(sftpConn, fPath)
	if err != nil {
		return err
	}

	// Recursively copy directories.
	if srcInfo.IsDir() {
		return sftpRecursivePullFile(sftpConn, srcInfo, fPath, normalizedPath, target, c.global.flagQuiet, c.puller.flagDereference, util.PathExists(target))
	}

	var targetPath string
	if targetIsDir {
		targetPath = filepath.Join(target, path.Base(normalizedPath))
	} else {
		targetPath = target
	}

	// Prepare target.
	targetIsLink := srcInfo.Mode()&os.ModeSymlink != 0
	var f *os.File
	var linkName string

	if isStdout(targetPath) {
		f = os.Stdout
	} else if targetIsLink {
		linkName, err = sftpConn.ReadLink(normalizedPath)
		if err != nil {
			return err
		}
	} else {
		f, err = os.Create(targetPath)
		if err != nil {
			return err
		}

		defer logger.WarnOnError(f.Close, "Failed to close file") // nolint:revive

		err = os.Chmod(targetPath, os.FileMode(srcInfo.Mode()))
		if err != nil {
			return err
		}
	}

	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), targetPath, normalizedPath),
		Quiet:  c.global.flagQuiet,
	}

	writer := &ioprogress.ProgressWriter{
		WriteCloser: f,
		Tracker: &ioprogress.ProgressTracker{
			Handler: func(bytesReceived int64, speed int64) {
				if isStdout(targetPath) {
					return
				}

				progress.UpdateProgress(ioprogress.ProgressData{
					Text: fmt.Sprintf("%s (%s/s)",
						units.GetByteSizeString(bytesReceived, 2),
						units.GetByteSizeString(speed, 2)),
				})
			},
		},
	}

	if targetIsLink {
		err = os.Symlink(linkName, targetPath)
		if err != nil {
			progress.Done("")
			return err
		}
	} else {
		src, err := sftpConn.Open(normalizedPath)
		if err != nil {
			return err
		}

		defer logger.WarnOnError(src.Close, "Failed to close source file")

		_, err = util.SafeCopy(writer, src)
		if err != nil {
			progress.Done("")
			return err
		}
	}

	progress.Done("")
	return nil
}

func (c *cmdStorageVolumeFilePull) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeFilePullUsage, cmd, args)
	if err != nil {
		return err
	}

	return c.pull(parsed[0], parsed[1], parsed[2].String)
}

// Push.
type cmdStorageVolumeFilePush struct {
	global            *cmdGlobal
	storage           *cmdStorage
	storageVolume     *cmdStorageVolume
	storageVolumeFile *cmdStorageVolumeFile
	pusher            *pushable

	edit         bool
	noModeChange bool
}

var cmdStorageVolumeFilePushUsage = u.Usage{u.Path, u.Pool.Remote(), u.MakePath(u.Volume, u.Target(u.Path))}

func (c *cmdStorageVolumeFilePush) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("push", cmdStorageVolumeFilePushUsage...)
	cmd.Short = i18n.G("Push files into custom volumes")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Push files into custom volumes`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus storage volume file push /etc/hosts local v1/etc/hosts
   To push /etc/hosts into the custom volume "v1".

echo "Hello world" | incus storage volume file push - local v1 test
   To read "Hello world" from standard input and write it into test in volume "v1".`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.storageVolumeFile.flagMkdir, "create-dirs|p", i18n.G("Create any directories necessary"))
	cli.AddIntFlag(cmd.Flags(), &c.storageVolumeFile.flagUID, "uid", -1, i18n.G("Set the file's uid on push"))
	cli.AddIntFlag(cmd.Flags(), &c.storageVolumeFile.flagGID, "gid", -1, i18n.G("Set the file's gid on push"))
	cli.AddStringFlag(cmd.Flags(), &c.storageVolumeFile.flagMode, "mode", "", "", i18n.G("Set the file's perms on push"))
	cli.AddBoolFlag(cmd.Flags(), &c.pusher.flagRecursive, "recursive|r", i18n.G("Recursively transfer files"))
	cli.AddBoolFlag(cmd.Flags(), &c.pusher.flagNoDereference, "no-dereference|P", i18n.G("Never follow symbolic links in source path"))
	cli.AddBoolFlag(cmd.Flags(), &c.pusher.flagFollow, "follow|H", i18n.G("Follow command-line symbolic links in source path"))
	cli.AddBoolFlag(cmd.Flags(), &c.pusher.flagDereference, "dereference|L", i18n.G("Always follow symbolic links in source path"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return c.global.cmpFiles(toComplete, true)
	}

	return cmd
}

// push runs the post-parsing command logic.
func (c *cmdStorageVolumeFilePush) push(srcFile string, parsedPool *u.Parsed, parsedTarget *u.Parsed) error {
	d := parsedPool.RemoteServer
	poolName := parsedPool.RemoteObject.String
	volName := parsedTarget.List[0].String
	target, targetIsDir := normalizePath(parsedTarget.List[1].String)
	targetExists := false

	// Connect to SFTP.
	sftpConn, err := d.GetStoragePoolVolumeFileSFTP(poolName, "custom", volName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
	}

	defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

	targetInfo, err := sftpConn.Stat(target)
	if err == nil {
		targetExists = true
		if targetInfo.IsDir() {
			targetIsDir = true
		} else if targetIsDir {
			// Let’s be extra careful and check that explicit requests for directories actually point to
			// directories.
			return fmt.Errorf(i18n.G("%s is not a directory"), target)
		}
	}

	mode := -1
	if c.storageVolumeFile.flagMode != "" {
		if len(c.storageVolumeFile.flagMode) == 3 {
			c.storageVolumeFile.flagMode = "0" + c.storageVolumeFile.flagMode
		}

		m, err := strconv.ParseInt(c.storageVolumeFile.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = int(os.FileMode(m).Perm())
	}

	// Push the files
	var f *os.File
	var linkTarget string
	var size int64
	args := incus.InstanceFileArgs{
		UID:  int64(c.storageVolumeFile.flagUID),
		GID:  int64(c.storageVolumeFile.flagGID),
		Mode: mode,
	}

	if isStdin(srcFile) {
		if targetIsDir {
			return errors.New(i18n.G("A target file name must be specified when pushing from stdin; the target is a directory"))
		}

		f = os.Stdin
	} else {
		srcInfo, wPath, err := c.pusher.statFile(srcFile)
		if err != nil {
			return err
		}

		// Recursively copy directories.
		if srcInfo.IsDir() {
			return sftpRecursivePushFile(sftpConn, wPath, srcFile, target, args, c.global.flagQuiet, c.pusher.flagDereference, targetExists)
		}

		if srcInfo.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(srcFile)
			if err != nil {
				return err
			}
		} else {
			f, err = os.Open(srcFile)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to open source file %q: %v"), f, err)
			}

			size = srcInfo.Size()
			defer logger.WarnOnError(f.Close, "Failed to close file")
		}

		dMode, dUID, dGID := internalIO.GetOwnerMode(srcInfo)

		if args.Mode == -1 {
			args.Mode = int(dMode)
		}

		if args.UID == -1 {
			args.UID = int64(dUID)
		}

		if args.GID == -1 {
			args.GID = int64(dGID)
		}
	}

	// Determine the target path.
	var targetPath string
	if targetIsDir {
		targetPath = path.Join(target, filepath.Base(srcFile))
	} else {
		targetPath = target
	}

	// Create needed paths if requested
	if c.storageVolumeFile.flagMkdir {
		mode := os.FileMode(DirMode)
		err = sftpRecursiveMkdir(sftpConn, path.Dir(targetPath), &mode, int64(args.UID), int64(args.GID))
		if err != nil {
			return err
		}
	}

	// Check if the path already exists.
	_, err = sftpConn.Stat(targetPath)
	if err == nil && c.noModeChange {
		args.UID = -1
		args.GID = -1
		args.Mode = -1
	}

	// Transfer the files.
	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), srcFile, targetPath),
		Quiet:  c.global.flagQuiet,
	}

	if f == nil {
		args.Type = "symlink"
		args.Content = strings.NewReader(linkTarget)
	} else {
		args.Type = "file"
		args.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Length: size,
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, f)
	}

	logger.Infof("Pushing %s to %s (%s)", srcFile, targetPath, args.Type)
	err = sftpCreateFile(sftpConn, targetPath, args, true)
	progress.Done("")
	return err
}

func (c *cmdStorageVolumeFilePush) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdStorageVolumeFilePushUsage, cmd, args)
	if err != nil {
		return err
	}

	return c.push(parsed[0].String, parsed[1], parsed[2])
}
