package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
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

const (
	// DirMode represents the file mode for creating dirs on `incus file pull/push`.
	DirMode = 0o755
	// FileMode represents the file mode for creating files on `incus file create`.
	FileMode = 0o644
)

type cmdFile struct {
	global *cmdGlobal

	flagUID  int
	flagGID  int
	flagMode string

	flagMkdir bool
}

func (c *cmdFile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("file")
	cmd.Short = i18n.G("Manage files in instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage files in instances`))

	// Create
	fileCreateCmd := cmdFileCreate{global: c.global, file: c}
	cmd.AddCommand(fileCreateCmd.command())

	// Delete
	fileDeleteCmd := cmdFileDelete{global: c.global, file: c}
	cmd.AddCommand(fileDeleteCmd.command())

	// Mount
	fileMountCmd := cmdFileMount{global: c.global, file: c}
	cmd.AddCommand(fileMountCmd.command())

	// Pull
	filePullCmd := cmdFilePull{global: c.global, file: c, puller: &pullable{}}
	cmd.AddCommand(filePullCmd.command())

	// Push
	filePushCmd := cmdFilePush{global: c.global, file: c, pusher: &pushable{}}
	cmd.AddCommand(filePushCmd.command())

	// Edit
	fileEditCmd := cmdFileEdit{global: c.global, file: c, filePull: &filePullCmd, filePush: &filePushCmd}
	cmd.AddCommand(fileEditCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdFileCreate struct {
	global *cmdGlobal
	file   *cmdFile

	flagForce bool
	flagType  string
}

var cmdFileCreateUsage = u.Usage{u.MakePath(u.Instance, u.Path).Remote(), u.SymlinkTargetPath.Optional()}

func (c *cmdFileCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdFileCreateUsage...)
	cmd.Short = i18n.G("Create files and directories in instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create files and directories in instances`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file create foo/bar
   To create a file /bar in the foo instance.

incus file create --type=symlink foo/bar baz
   To create a symlink /bar in instance foo whose target is baz.`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.file.flagMkdir, "create-dirs|p", i18n.G("Create any directories necessary"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagForce, "force|f", i18n.G("Force creating files or directories"))
	cli.AddIntFlag(cmd.Flags(), &c.file.flagGID, "gid", -1, i18n.G("Set the file's gid on create"))
	cli.AddIntFlag(cmd.Flags(), &c.file.flagUID, "uid", -1, i18n.G("Set the file's uid on create"))
	cli.AddStringFlag(cmd.Flags(), &c.file.flagMode, "mode", "", "", i18n.G("Set the file's perms on create"))
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

func (c *cmdFileCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdFileCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.List[0].String
	targetPath, isDir := normalizePath(parsed[0].RemoteObject.List[1].String)
	hasSymlink := !parsed[1].Skipped
	symlinkTargetPath := parsed[1].String

	if !slices.Contains([]string{"file", "symlink", "directory"}, c.flagType) {
		return fmt.Errorf(i18n.G("Invalid type %q"), c.flagType)
	}

	if hasSymlink {
		if c.flagType != "symlink" {
			return errors.New(i18n.G(`Symlink target path can only be used for type "symlink"`))
		}

		symlinkTargetPath = filepath.Clean(symlinkTargetPath)
	}

	if isDir {
		c.flagType = "directory"
	}

	// Connect to SFTP.
	sftpConn, err := d.GetInstanceFileSFTP(instanceName)
	if err != nil {
		return err
	}

	defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

	// Determine the target uid
	uid := max(c.file.flagUID, 0)

	// Determine the target gid
	gid := max(c.file.flagGID, 0)

	var mode os.FileMode

	// Determine the target mode
	switch c.flagType {
	case "directory":
		mode = os.FileMode(DirMode)
	case "file":
		mode = os.FileMode(FileMode)
	}

	if c.file.flagMode != "" {
		if len(c.file.flagMode) == 3 {
			c.file.flagMode = "0" + c.file.flagMode
		}

		m, err := strconv.ParseInt(c.file.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = os.FileMode(m)
	}

	// Create needed paths if requested
	if c.file.flagMkdir {
		err = sftpRecursiveMkdir(sftpConn, filepath.Dir(targetPath), nil, int64(uid), int64(gid))
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

	err = sftpCreateFile(sftpConn, targetPath, fileArgs, false)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

// Delete.
type cmdFileDelete struct {
	global *cmdGlobal
	file   *cmdFile

	flagForce bool
}

var cmdFileDeleteUsage = u.Usage{u.MakePath(u.Instance, u.Path).Remote().List(1)}

func (c *cmdFileDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdFileDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete files in instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete files in instances`))

	cli.AddBoolFlag(cmd.Flags(), &c.flagForce, "force|f", i18n.G("Force deleting files, directories, and subdirectories"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpFiles(toComplete, false)
	}

	return cmd
}

func (c *cmdFileDelete) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdFileDeleteUsage, cmd, args)
	if err != nil {
		return err
	}

	// Store clients.
	sftpClients := map[string]*sftp.Client{}

	defer func() {
		for _, sftpClient := range sftpClients {
			_ = sftpClient.Close()
		}
	}()

	var errs []error

	for _, p := range parsed[0].List {
		err := func() error {
			d := p.RemoteServer
			instanceName := p.RemoteObject.List[0].String
			path, _ := normalizePath(p.RemoteObject.List[1].String)
			instanceID := p.RemoteName + ":" + instanceName

			sftpConn, ok := sftpClients[instanceID]
			if !ok {
				sftpConn, err = d.GetInstanceFileSFTP(instanceName)
				if err != nil {
					return err
				}

				sftpClients[instanceID] = sftpConn
			}

			if c.flagForce {
				err = sftpConn.RemoveAll(path)
				if err != nil {
					return err
				}

				return nil
			}

			return sftpConn.Remove(path)
		}()
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Edit.
type cmdFileEdit struct {
	global   *cmdGlobal
	file     *cmdFile
	filePull *cmdFilePull
	filePush *cmdFilePush
}

var cmdFileEditUsage = u.Usage{u.MakePath(u.Instance, u.Path).Remote()}

func (c *cmdFileEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdFileEditUsage...)
	cmd.Short = i18n.G("Edit files in instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit files in instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdFileEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdFileEditUsage, cmd, args)
	if err != nil {
		return err
	}

	fileName := parsed[0].RemoteObject.List[1].String
	c.filePush.noModeChange = true

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		return c.filePush.push([]string{os.Stdin.Name()}, parsed[0])
	}

	// Create temp file
	f, err := os.CreateTemp("", fmt.Sprintf("incus_file_edit_*%s", filepath.Ext(fileName)))
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
	err = c.filePull.pull(parsed, fname)
	if err != nil {
		return err
	}

	// Spawn the editor
	_, err = cli.TextEditor(fname, []byte{})
	if err != nil {
		return err
	}

	// Push the result
	err = c.filePush.push([]string{fname}, parsed[0])
	if err != nil {
		return err
	}

	return nil
}

// Pull.
type cmdFilePull struct {
	global *cmdGlobal
	file   *cmdFile
	puller *pullable

	edit bool
}

var cmdFilePullUsage = u.Usage{u.MakePath(u.Instance, u.Path).Remote().List(1), u.Target(u.Path)}

func (c *cmdFilePull) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("pull", cmdFilePullUsage...)
	cmd.Short = i18n.G("Pull files from instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Pull files from instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file pull foo/etc/hosts .
   To pull /etc/hosts from the instance and write it to the current directory.

incus file pull foo/etc/hosts -
   To pull /etc/hosts from the instance and write its output to standard output.`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.file.flagMkdir, "create-dirs|p", i18n.G("Create any directories necessary"))
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
func (c *cmdFilePull) pull(parsedFiles []*u.Parsed, target string) error {
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
	 * directory in one of three cases:
	 *   1. Someone explicitly put "/" at the end
	 *   2. Someone provided more than one source. In this case the target
	 *      should be a directory so we can save all the files into it.
	 *   3. We are dealing with recursive copy
	 */
	if targetExists {
		targetIsDir = targetInfo.IsDir()
		if !targetIsDir && len(parsedFiles) > 1 {
			return errors.New(i18n.G("More than one file to download, but target is not a directory"))
		}
	} else if targetIsDir || len(parsedFiles) > 1 {
		err := os.MkdirAll(target, DirMode)
		if err != nil {
			return err
		}

		targetIsDir = true
	} else if c.file.flagMkdir {
		err := os.MkdirAll(filepath.Dir(target), DirMode)
		if err != nil {
			return err
		}
	}

	sftpClients := map[string]*sftp.Client{}

	defer func() {
		for _, sftpClient := range sftpClients {
			_ = sftpClient.Close()
		}
	}()

	var errs []error

	for _, p := range parsedFiles {
		err := func() error {
			d := p.RemoteServer
			instanceName := p.RemoteObject.List[0].String
			path := p.RemoteObject.List[1].String
			instanceID := p.RemoteName + ":" + instanceName

			sftpConn, ok := sftpClients[instanceID]
			if !ok {
				sftpConn, err = d.GetInstanceFileSFTP(instanceName)
				if err != nil {
					return err
				}

				sftpClients[instanceID] = sftpConn
			}

			srcInfo, normalizedPath, err := c.puller.statFile(sftpConn, path)
			if err != nil {
				return err
			}

			// Recursively copy directories.
			if srcInfo.IsDir() {
				return sftpRecursivePullFile(sftpConn, srcInfo, path, normalizedPath, target, c.global.flagQuiet, c.puller.flagDereference, len(parsedFiles) > 1 || util.PathExists(target))
			}

			// Determine the target path.
			var targetPath string
			if targetIsDir {
				targetPath = filepath.Join(target, filepath.Base(normalizedPath))
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
		}()
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *cmdFilePull) run(cmd *cobra.Command, args []string) error {
	// Do NOT blindly copy the following parsing line; it performs right-to-left parsing, which in
	// most cases is NOT what you want.
	parsed, err := c.global.Parse(cmdFilePullUsage, cmd, args, true)
	if err != nil {
		return err
	}

	return c.pull(parsed[0].List, parsed[1].String)
}

// Push.
type cmdFilePush struct {
	global *cmdGlobal
	file   *cmdFile
	pusher *pushable

	edit         bool
	noModeChange bool
}

var cmdFilePushUsage = u.Usage{u.Path.List(1), u.MakePath(u.Instance, u.Target(u.Path)).Remote()}

func (c *cmdFilePush) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("push", cmdFilePushUsage...)
	cmd.Short = i18n.G("Push files into instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Push files into instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file push /etc/hosts foo/etc/hosts
   To push /etc/hosts into the instance "foo".

echo "Hello world" | incus file push - foo/root/test
   To read "Hello world" from standard input and write it into /root/test in instance "foo".`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.file.flagMkdir, "create-dirs|p", i18n.G("Create any directories necessary"))
	cli.AddIntFlag(cmd.Flags(), &c.file.flagUID, "uid", -1, i18n.G("Set the files' UIDs on push"))
	cli.AddIntFlag(cmd.Flags(), &c.file.flagGID, "gid", -1, i18n.G("Set the files' GIDs on push"))
	cli.AddStringFlag(cmd.Flags(), &c.file.flagMode, "mode", "", "", i18n.G("Set the file's perms on push (in recursive mode, only sets the target directory's permissions)"))
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
func (c *cmdFilePush) push(srcFiles []string, parsedTarget *u.Parsed) error {
	d := parsedTarget.RemoteServer
	instanceName := parsedTarget.RemoteObject.List[0].String
	target, targetIsDir := normalizePath(parsedTarget.RemoteObject.List[1].String)
	targetExists := false

	err := c.pusher.preCheck()
	if err != nil {
		return err
	}

	// Connect to SFTP.
	sftpConn, err := d.GetInstanceFileSFTP(instanceName)
	if err != nil {
		return err
	}

	defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

	targetInfo, err := sftpConn.Stat(target)
	if err == nil {
		targetExists = true
		if targetInfo.IsDir() {
			targetIsDir = true
		} else if len(srcFiles) > 1 || targetIsDir {
			// Let’s be extra careful and check that explicit requests for directories actually point to
			// directories.
			return fmt.Errorf(i18n.G("%s is not a directory"), target)
		}
	} else if len(srcFiles) > 1 && !c.file.flagMkdir {
		return errors.New(i18n.G("Missing target directory"))
	}

	mode := -1
	if c.file.flagMode != "" {
		if len(c.file.flagMode) == 3 {
			c.file.flagMode = "0" + c.file.flagMode
		}

		m, err := strconv.ParseInt(c.file.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = int(os.FileMode(m).Perm())
	}

	var errs []error
	canProcessStdin := len(srcFiles) == 1

	// Push the files
	for _, path := range srcFiles {
		err := func() error {
			var f *os.File
			var linkTarget string
			var size int64
			args := incus.InstanceFileArgs{
				UID:  int64(c.file.flagUID),
				GID:  int64(c.file.flagGID),
				Mode: mode,
			}

			if isStdin(path) {
				if !canProcessStdin {
					return errors.New(i18n.G("stdin can only be used once, with no other source arguments"))
				}

				if targetIsDir {
					return errors.New(i18n.G("A target file name must be specified when pushing from stdin; the target is a directory"))
				}

				canProcessStdin = false
				f = os.Stdin
			} else {
				srcInfo, wPath, err := c.pusher.statFile(path)
				if err != nil {
					return err
				}

				// Recursively copy directories.
				if srcInfo.IsDir() {
					return sftpRecursivePushFile(sftpConn, wPath, path, target, args, c.global.flagQuiet, c.pusher.flagDereference, len(srcFiles) > 1 || targetExists)
				}

				if srcInfo.Mode()&os.ModeSymlink != 0 {
					linkTarget, err = os.Readlink(path)
					if err != nil {
						return err
					}
				} else {
					f, err = os.Open(path)
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
				targetPath = filepath.Join(target, filepath.Base(path))
			} else {
				targetPath = target
			}

			// Create needed paths if requested
			if c.file.flagMkdir {
				mode := os.FileMode(DirMode)
				err = sftpRecursiveMkdir(sftpConn, filepath.Dir(targetPath), &mode, int64(args.UID), int64(args.GID))
				if err != nil {
					return err
				}
			}

			// Check if the path already exists.
			_, err := sftpConn.Stat(targetPath)
			if err == nil && c.noModeChange {
				args.UID = -1
				args.GID = -1
				args.Mode = -1
			}

			// Transfer the files.
			progress := cli.ProgressRenderer{
				Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), path, targetPath),
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

			logger.Infof("Pushing %s to %s (%s)", path, targetPath, args.Type)
			err = sftpCreateFile(sftpConn, targetPath, args, true)
			progress.Done("")
			return err
		}()
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *cmdFilePush) run(cmd *cobra.Command, args []string) error {
	// Do NOT blindly copy the following parsing line; it performs right-to-left parsing, which in
	// most cases is NOT what you want.
	parsed, err := c.global.Parse(cmdFilePushUsage, cmd, args, true)
	if err != nil {
		return err
	}

	return c.push(parsed[0].StringList, parsed[1])
}

// Mount.
type cmdFileMount struct {
	global *cmdGlobal
	file   *cmdFile

	flagListen   string
	flagAuthNone bool
	flagAuthUser string
}

var cmdFileMountUsage = u.Usage{u.MakePath(u.Instance, u.Path.Optional()).Remote(), u.Target(u.Path).Optional()}

func (c *cmdFileMount) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("mount", cmdFileMountUsage...)
	cmd.Short = i18n.G("Mount files from instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Mount files from instances.
If no target path is provided, start an SSH SFTP listener instead.`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file mount foo/root fooroot
   To mount /root from the instance foo onto the local fooroot directory.

incus file mount foo
   To start an SSH SFTP listener for the root filesystem of instance foo.`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagListen, "listen", "", "", i18n.G("Setup SSH SFTP listener on address:port instead of mounting"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAuthNone, "no-auth", i18n.G("Disable authentication when using SSH SFTP listener"))
	cli.AddStringFlag(cmd.Flags(), &c.flagAuthUser, "auth-user", "", "", i18n.G("Set authentication user when using SSH SFTP listener"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		if len(args) == 1 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdFileMount) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdFileMountUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.List[0].String
	hasInstancePath := !parsed[0].RemoteObject.List[1].Skipped
	instancePath := parsed[0].RemoteObject.List[1].String
	hasTarget := !parsed[1].Skipped
	targetPath := filepath.Clean(parsed[1].String)

	// Determine the target if specified.
	if hasTarget {
		sb, err := os.Stat(targetPath)
		if err != nil {
			return err
		}

		if !sb.IsDir() {
			return errors.New(i18n.G("Target path must be a directory"))
		}
	}

	// Check which mode we should operate in. If target path is provided we use sshfs mode.
	if hasTarget && c.flagListen != "" {
		return errors.New(i18n.G("Target path and --listen flag cannot be used together"))
	}

	// Check instance path is provided in sshfs mode.
	if !hasInstancePath && hasTarget {
		return fmt.Errorf(i18n.G("An instance path is required for %s"), formatRemote(c.global.conf, parsed[0]))
	}

	// Check instance path isn't provided in listener mode.
	if hasInstancePath && !hasTarget {
		return errors.New(i18n.G("Instance path cannot be used in SSH SFTP listener mode"))
	}

	// Look for sshfs command if no SSH SFTP listener mode specified and a target mount path was specified.
	if c.flagListen == "" && hasTarget {
		// Setup sourcePath with leading / to ensure we reference the instance path from / location.
		if len(instancePath) == 0 || instancePath[0] != '/' {
			instancePath = "/" + instancePath
		}

		// Connect to SFTP.
		sftpConn, err := d.GetInstanceFileSFTPConn(instanceName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
		}

		defer logger.WarnOnError(sftpConn.Close, "Failed to close SFTP connection")

		return sshfsMount(cmd.Context(), sftpConn, instanceName, instancePath, targetPath)
	}

	// Check the instance exists before starting the SFTP server.
	_, _, err = d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	return sshSFTPServer(cmd.Context(), func() (net.Conn, error) { return d.GetInstanceFileSFTPConn(instanceName) }, c.flagAuthNone, c.flagAuthUser, c.flagListen)
}
