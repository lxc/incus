package main

import (
	"bytes"
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

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	internalIO "github.com/lxc/incus/v6/internal/io"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
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

	flagMkdir     bool
	flagRecursive bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdFile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("file")
	cmd.Short = i18n.G("Manage files in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage files in instances`))

	// Create
	fileCreateCmd := cmdFileCreate{global: c.global, file: c}
	cmd.AddCommand(fileCreateCmd.Command())

	// Delete
	fileDeleteCmd := cmdFileDelete{global: c.global, file: c}
	cmd.AddCommand(fileDeleteCmd.Command())

	// Mount
	fileMountCmd := cmdFileMount{global: c.global, file: c}
	cmd.AddCommand(fileMountCmd.Command())

	// Pull
	filePullCmd := cmdFilePull{global: c.global, file: c}
	cmd.AddCommand(filePullCmd.Command())

	// Push
	filePushCmd := cmdFilePush{global: c.global, file: c}
	cmd.AddCommand(filePushCmd.Command())

	// Edit
	fileEditCmd := cmdFileEdit{global: c.global, file: c, filePull: &filePullCmd, filePush: &filePushCmd}
	cmd.AddCommand(fileEditCmd.Command())

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

// Command returns the cobra command for `file create`.
func (c *cmdFileCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<instance>/<path> [<symlink target path>]"))
	cmd.Short = i18n.G("Create files and directories in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create files and directories in instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file create foo/bar
   To create a file /bar in the foo instance.

incus file create --type=symlink foo/bar baz
   To create a symlink /bar in instance foo whose target is baz.`))

	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary")+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force creating files or directories")+"``")
	cmd.Flags().IntVar(&c.file.flagGID, "gid", -1, i18n.G("Set the file's gid on create")+"``")
	cmd.Flags().IntVar(&c.file.flagUID, "uid", -1, i18n.G("Set the file's uid on create")+"``")
	cmd.Flags().StringVar(&c.file.flagMode, "mode", "", i18n.G("Set the file's perms on create")+"``")
	cmd.Flags().StringVar(&c.flagType, "type", "file", i18n.G("The type to create (file, symlink, or directory)")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the `file create` command.
func (c *cmdFileCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	if !slices.Contains([]string{"file", "symlink", "directory"}, c.flagType) {
		return fmt.Errorf(i18n.G("Invalid type %q"), c.flagType)
	}

	if len(args) == 2 && c.flagType != "symlink" {
		return errors.New(i18n.G(`Symlink target path can only be used for type "symlink"`))
	}

	if strings.HasSuffix(args[0], "/") {
		c.flagType = "directory"
	}

	pathSpec := strings.SplitN(args[0], "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf(i18n.G("Invalid target %s"), args[0])
	}

	// Parse remote.
	resources, err := c.global.parseServers(pathSpec[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Connect to SFTP.
	sftpConn, err := resource.server.GetInstanceFileSFTP(resource.name)
	if err != nil {
		return err
	}

	defer func() { _ = sftpConn.Close() }()

	// re-add leading / that got stripped by the SplitN
	targetPath := filepath.Clean("/" + pathSpec[1])

	// normalization may reveal that path is still a dir, e.g. /.
	if strings.HasSuffix(targetPath, "/") {
		c.flagType = "directory"
	}

	var symlinkTargetPath string

	// Determine the target if specified.
	if len(args) == 2 {
		symlinkTargetPath = filepath.Clean(args[1])
	}

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
		err = c.file.recursiveMkdir(sftpConn, filepath.Dir(targetPath), nil, int64(uid), int64(gid))
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

	err = c.file.sftpCreateFile(sftpConn, targetPath, fileArgs, false)
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

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdFileDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance>/<path> [[<remote>:]<instance>/<path>...]"))
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete files in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete files in instances`))

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force deleting files, directories, and subdirectories")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpFiles(toComplete, false)
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdFileDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args...)
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

	for _, resource := range resources {
		pathSpec := strings.SplitN(resource.name, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid path %s"), resource.name)
		}

		sftpConn, ok := sftpClients[pathSpec[0]]
		if !ok {
			sftpConn, err = resource.server.GetInstanceFileSFTP(pathSpec[0])
			if err != nil {
				return err
			}

			sftpClients[pathSpec[0]] = sftpConn
		}

		if c.flagForce {
			err = sftpConn.RemoveAll(pathSpec[1])
			if err != nil {
				return err
			}

			return nil
		}

		err = sftpConn.Remove(pathSpec[1])
		if err != nil {
			return err
		}
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

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdFileEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<instance>/<path>"))
	cmd.Short = i18n.G("Edit files in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit files in instances`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdFileEdit) Run(cmd *cobra.Command, args []string) error {
	c.filePush.noModeChange = true

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		return c.filePush.Run(cmd, append([]string{os.Stdin.Name()}, args[0]))
	}

	// Create temp file
	f, err := os.CreateTemp("", fmt.Sprintf("incus_file_edit_*%s", filepath.Ext(args[0])))
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
	defer func() { _ = os.Remove(fname) }()
	err = c.filePull.Run(cmd, append([]string{args[0]}, fname))
	if err != nil {
		return err
	}

	// Spawn the editor
	_, err = textEditor(fname, []byte{})
	if err != nil {
		return err
	}

	// Push the result
	err = c.filePush.Run(cmd, append([]string{fname}, args[0]))
	if err != nil {
		return err
	}

	return nil
}

// Pull.
type cmdFilePull struct {
	global *cmdGlobal
	file   *cmdFile

	edit bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdFilePull) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("pull", i18n.G("[<remote>:]<instance>/<path> [[<remote>:]<instance>/<path>...] <target path>"))
	cmd.Short = i18n.G("Pull files from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Pull files from instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file pull foo/etc/hosts .
   To pull /etc/hosts from the instance and write it to the current directory.

incus file pull foo/etc/hosts -
   To pull /etc/hosts from the instance and write its output to standard output.`))

	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().BoolVarP(&c.file.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpFiles(toComplete, false)
		}

		return c.global.cmpFiles(toComplete, true)
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdFilePull) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Determine the target
	target := filepath.Clean(args[len(args)-1])

	targetIsDir := false
	targetIsLink := false

	targetInfo, err := os.Stat(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	if err == nil {
		targetIsDir = targetInfo.IsDir()
		if !targetIsDir && len(args)-1 > 1 {
			return errors.New(i18n.G("More than one file to download, but target is not a directory"))
		}
	} else if strings.HasSuffix(args[len(args)-1], string(os.PathSeparator)) || len(args)-1 > 1 {
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

	// Parse remote
	resources, err := c.global.parseServers(args[:len(args)-1]...)
	if err != nil {
		return err
	}

	sftpClients := map[string]*sftp.Client{}

	defer func() {
		for _, sftpClient := range sftpClients {
			_ = sftpClient.Close()
		}
	}()

	for _, resource := range resources {
		pathSpec := strings.SplitN(resource.name, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid source %s"), resource.name)
		}

		// Make sure we have a leading / for the path.
		if !strings.HasPrefix(pathSpec[1], "/") {
			pathSpec[1] = "/" + pathSpec[1]
		}

		sftpConn, ok := sftpClients[pathSpec[0]]
		if !ok {
			sftpConn, err = resource.server.GetInstanceFileSFTP(pathSpec[0])
			if err != nil {
				return err
			}

			sftpClients[pathSpec[0]] = sftpConn
		}

		src, err := sftpConn.Open(pathSpec[1])
		if err != nil {
			return err
		}

		srcInfo, err := sftpConn.Lstat(pathSpec[1])
		if err != nil {
			return err
		}

		if srcInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			targetIsLink = true
		}

		// Deal with recursion
		if srcInfo.IsDir() {
			if c.file.flagRecursive {
				if !util.PathExists(target) {
					err := os.MkdirAll(target, DirMode)
					if err != nil {
						return err
					}

					targetIsDir = true
				}

				err := c.file.recursivePullFile(sftpConn, pathSpec[1], target)
				if err != nil {
					return err
				}

				continue
			}

			return errors.New(i18n.G("Can't pull a directory without --recursive"))
		}

		var targetPath string
		if targetIsDir {
			targetPath = filepath.Join(target, filepath.Base(pathSpec[1]))
		} else {
			targetPath = target
		}

		var f *os.File
		var linkName string

		if targetPath == "-" {
			f = os.Stdout
		} else if targetIsLink {
			linkName, err = sftpConn.ReadLink(pathSpec[1])
			if err != nil {
				return err
			}
		} else {
			f, err = os.Create(targetPath)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }() // nolint:revive

			err = os.Chmod(targetPath, os.FileMode(srcInfo.Mode()))
			if err != nil {
				return err
			}
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), targetPath, pathSpec[1]),
			Quiet:  c.global.flagQuiet,
		}

		writer := &ioprogress.ProgressWriter{
			WriteCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(bytesReceived int64, speed int64) {
					if targetPath == "-" {
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
			err = os.Symlink(linkName, srcInfo.Name())
			if err != nil {
				progress.Done("")
				return err
			}
		} else {
			for {
				// Read 1MB at a time.
				_, err = io.CopyN(writer, src, 1024*1024)
				if err != nil {
					if err == io.EOF {
						break
					}

					progress.Done("")
					return err
				}
			}
		}

		progress.Done("")
	}

	return nil
}

// Push.
type cmdFilePush struct {
	global *cmdGlobal
	file   *cmdFile

	edit         bool
	noModeChange bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdFilePush) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("push", i18n.G("<source path>... [<remote>:]<instance>/<path>"))
	cmd.Short = i18n.G("Push files into instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Push files into instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file push /etc/hosts foo/etc/hosts
   To push /etc/hosts into the instance "foo".

echo "Hello world" | incus file push - foo/root/test
   To read "Hello world" from standard input and write it into /roo/test in instance "foo".`))

	cmd.Flags().BoolVarP(&c.file.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().IntVar(&c.file.flagUID, "uid", -1, i18n.G("Set the file's uid on push")+"``")
	cmd.Flags().IntVar(&c.file.flagGID, "gid", -1, i18n.G("Set the file's gid on push")+"``")
	cmd.Flags().StringVar(&c.file.flagMode, "mode", "", i18n.G("Set the file's perms on push")+"``")

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return c.global.cmpFiles(toComplete, true)
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdFilePush) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse the destination
	target := args[len(args)-1]
	pathSpec := strings.SplitN(target, "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf(i18n.G("Invalid target %s"), target)
	}

	targetIsDir := strings.HasSuffix(target, "/")
	// re-add leading / that got stripped by the SplitN
	targetPath := "/" + pathSpec[1]
	// clean various /./, /../, /////, etc. that users add (#2557)
	targetPath = filepath.Clean(targetPath)

	// normalization may reveal that path is still a dir, e.g. /.
	if strings.HasSuffix(targetPath, "/") {
		targetIsDir = true
	}

	// Parse remote
	resources, err := c.global.parseServers(pathSpec[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Connect to SFTP.
	sftpConn, err := resource.server.GetInstanceFileSFTP(resource.name)
	if err != nil {
		return err
	}

	defer func() { _ = sftpConn.Close() }()

	// Make a list of paths to transfer
	sourcefilenames := []string{}
	for _, fname := range args[:len(args)-1] {
		sourcefilenames = append(sourcefilenames, filepath.Clean(fname))
	}

	// Determine the target mode
	mode := os.FileMode(DirMode)
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

	// Recursive calls
	if c.file.flagRecursive {
		// Quick checks.
		if c.file.flagUID != -1 || c.file.flagGID != -1 || c.file.flagMode != "" {
			return errors.New(i18n.G("Can't supply uid/gid/mode in recursive mode"))
		}

		// Create needed paths if requested
		if c.file.flagMkdir {
			f, err := os.Open(sourcefilenames[0])
			if err != nil {
				return err
			}

			finfo, err := f.Stat()
			_ = f.Close()
			if err != nil {
				return err
			}

			mode, uid, gid := internalIO.GetOwnerMode(finfo)

			err = c.file.recursiveMkdir(sftpConn, targetPath, &mode, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		// Transfer the files
		for _, fname := range sourcefilenames {
			err := c.file.recursivePushFile(sftpConn, fname, targetPath)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Determine the target uid
	uid := max(c.file.flagUID, 0)

	// Determine the target gid
	gid := max(c.file.flagGID, 0)

	if (len(sourcefilenames) > 1) && !targetIsDir {
		return errors.New(i18n.G("Missing target directory"))
	}

	// Make sure all of the files are accessible by us before trying to push any of them
	var files []*os.File
	for _, f := range sourcefilenames {
		var file *os.File
		if f == "-" {
			file = os.Stdin
		} else {
			file, err = os.Open(f)
			if err != nil {
				return err
			}
		}

		defer func() { _ = file.Close() }() // nolint:revive
		files = append(files, file)
	}

	// Push the files
	for _, f := range files {
		fpath := targetPath
		if targetIsDir {
			fpath = filepath.Join(fpath, filepath.Base(f.Name()))
		}

		// Create needed paths if requested
		if c.file.flagMkdir {
			finfo, err := f.Stat()
			if err != nil {
				return err
			}

			if c.file.flagUID == -1 || c.file.flagGID == -1 {
				_, dUID, dGID := internalIO.GetOwnerMode(finfo)

				if c.file.flagUID == -1 {
					uid = dUID
				}

				if c.file.flagGID == -1 {
					gid = dGID
				}
			}

			err = c.file.recursiveMkdir(sftpConn, filepath.Dir(fpath), nil, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		// Transfer the files
		args := incus.InstanceFileArgs{
			UID:  -1,
			GID:  -1,
			Mode: -1,
		}

		if !c.noModeChange {
			if c.file.flagMode == "" || c.file.flagUID == -1 || c.file.flagGID == -1 {
				finfo, err := f.Stat()
				if err != nil {
					return err
				}

				fMode, fUID, fGID := internalIO.GetOwnerMode(finfo)

				if c.file.flagMode == "" {
					mode = fMode
				}

				if c.file.flagUID == -1 {
					uid = fUID
				}

				if c.file.flagGID == -1 {
					gid = fGID
				}
			}

			args.UID = int64(uid)
			args.GID = int64(gid)
			args.Mode = int(mode.Perm())
		}

		args.Type = "file"

		fstat, err := f.Stat()
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), f.Name(), fpath),
			Quiet:  c.global.flagQuiet,
		}

		args.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, f)

		logger.Infof("Pushing %s to %s (%s)", f.Name(), fpath, args.Type)
		err = c.file.sftpCreateFile(sftpConn, fpath, args, true)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	}

	return nil
}

func (c *cmdFile) setOwnerMode(sftpConn *sftp.Client, targetPath string, args incus.InstanceFileArgs) error {
	// Skip if not on UNIX.
	_, err := sftpConn.StatVFS("/")
	if err != nil {
		return nil
	}

	// Get the current stat information.
	st, err := sftpConn.Stat(targetPath)
	if err != nil {
		return err
	}

	fileStat, ok := st.Sys().(*sftp.FileStat)
	if !ok {
		return fmt.Errorf("Invalid filestat data for %q", targetPath)
	}

	// Set owner.
	if args.UID >= 0 || args.GID >= 0 {
		if args.UID == -1 {
			args.UID = int64(fileStat.UID)
		}

		if args.GID == -1 {
			args.GID = int64(fileStat.GID)
		}

		err = sftpConn.Chown(targetPath, int(args.UID), int(args.GID))
		if err != nil {
			return err
		}
	}

	// Set mode.
	if args.Mode >= 0 {
		err = sftpConn.Chmod(targetPath, fs.FileMode(args.Mode))
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdFile) sftpCreateFile(sftpConn *sftp.Client, targetPath string, args incus.InstanceFileArgs, push bool) error {
	switch args.Type {
	case "file":
		file, err := sftpConn.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return err
		}

		defer func() { _ = file.Close() }()

		if push {
			for {
				// Read 1MB at a time.
				_, err = io.CopyN(file, args.Content, 1024*1024)
				if err != nil {
					if err == io.EOF {
						break
					}

					return err
				}
			}
		}

		err = c.setOwnerMode(sftpConn, targetPath, args)
		if err != nil {
			return err
		}

	case "directory":
		err := sftpConn.MkdirAll(targetPath)
		if err != nil {
			return err
		}

		err = c.setOwnerMode(sftpConn, targetPath, args)
		if err != nil {
			return err
		}

	case "symlink":
		// If already a symlink, re-create it.
		fInfo, err := sftpConn.Lstat(targetPath)
		if err == nil && fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			err = sftpConn.Remove(targetPath)
			if err != nil {
				return err
			}
		}

		dest, err := io.ReadAll(args.Content)
		if err != nil {
			return err
		}

		err = sftpConn.Symlink(string(dest), targetPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *cmdFile) recursivePullFile(sftpConn *sftp.Client, p string, targetDir string) error {
	fInfo, err := sftpConn.Lstat(p)
	if err != nil {
		return err
	}

	var fileType string
	if fInfo.IsDir() {
		fileType = "directory"
	} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
		fileType = "symlink"
	} else {
		fileType = "file"
	}

	target := filepath.Join(targetDir, filepath.Base(p))
	logger.Infof("Pulling %s from %s (%s)", target, p, fileType)

	if fileType == "directory" {
		err := os.Mkdir(target, fInfo.Mode())
		if err != nil {
			return err
		}

		entries, err := sftpConn.ReadDir(p)
		if err != nil {
			return err
		}

		for _, ent := range entries {
			nextP := filepath.Join(p, ent.Name())

			err := c.recursivePullFile(sftpConn, nextP, target)
			if err != nil {
				return err
			}
		}
	} else if fileType == "file" {
		src, err := sftpConn.Open(p)
		if err != nil {
			return err
		}

		defer func() { _ = src.Close() }()

		dst, err := os.Create(target)
		if err != nil {
			return err
		}

		defer func() { _ = dst.Close() }()

		err = os.Chmod(target, fInfo.Mode())
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), p, target),
			Quiet:  c.global.flagQuiet,
		}

		writer := &ioprogress.ProgressWriter{
			WriteCloser: dst,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(bytesReceived int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%s (%s/s)",
							units.GetByteSizeString(bytesReceived, 2),
							units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}

		for {
			// Read 1MB at a time.
			_, err = io.CopyN(writer, src, 1024*1024)
			if err != nil {
				if err == io.EOF {
					break
				}

				progress.Done("")
				return err
			}
		}

		err = src.Close()
		if err != nil {
			progress.Done("")
			return err
		}

		err = dst.Close()
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	} else if fileType == "symlink" {
		linkTarget, err := sftpConn.ReadLink(p)
		if err != nil {
			return err
		}

		err = os.Symlink(linkTarget, target)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf(i18n.G("Unknown file type '%s'"), fileType)
	}

	return nil
}

func (c *cmdFile) recursivePushFile(sftpConn *sftp.Client, source string, target string) error {
	source = filepath.Clean(source)

	sourceDir, _ := filepath.Split(source)
	sourceLen := len(sourceDir)

	// Special handling for relative paths.
	if source == ".." {
		sourceLen = 1
	}

	sendFile := func(p string, fInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
		}

		// Detect unsupported files
		if !fInfo.Mode().IsRegular() && !fInfo.Mode().IsDir() && fInfo.Mode()&os.ModeSymlink != os.ModeSymlink {
			return fmt.Errorf(i18n.G("'%s' isn't a supported file type"), p)
		}

		// Prepare for file transfer
		targetPath := filepath.Join(target, filepath.ToSlash(p[sourceLen:]))
		mode, uid, gid := internalIO.GetOwnerMode(fInfo)
		args := incus.InstanceFileArgs{
			UID:  int64(uid),
			GID:  int64(gid),
			Mode: int(mode.Perm()),
		}

		var readCloser io.ReadCloser

		if fInfo.IsDir() {
			// Directory handling
			args.Type = "directory"
		} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			// Symlink handling
			symlinkTarget, err := os.Readlink(p)
			if err != nil {
				return err
			}

			args.Type = "symlink"
			args.Content = bytes.NewReader([]byte(symlinkTarget))
			readCloser = io.NopCloser(args.Content)
		} else {
			// File handling
			f, err := os.Open(p)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }()

			args.Type = "file"
			args.Content = f
			readCloser = f
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), p, targetPath),
			Quiet:  c.global.flagQuiet,
		}

		if args.Type != "directory" {
			contentLength, err := args.Content.Seek(0, io.SeekEnd)
			if err != nil {
				return err
			}

			_, err = args.Content.Seek(0, io.SeekStart)
			if err != nil {
				return err
			}

			args.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
				ReadCloser: readCloser,
				Tracker: &ioprogress.ProgressTracker{
					Length: contentLength,
					Handler: func(percent int64, speed int64) {
						progress.UpdateProgress(ioprogress.ProgressData{
							Text: fmt.Sprintf("%d%% (%s/s)", percent,
								units.GetByteSizeString(speed, 2)),
						})
					},
				},
			}, args.Content)
		}

		logger.Infof("Pushing %s to %s (%s)", p, targetPath, args.Type)
		err = c.sftpCreateFile(sftpConn, targetPath, args, true)
		if err != nil {
			if args.Type != "directory" {
				progress.Done("")
			}

			return err
		}

		if args.Type != "directory" {
			progress.Done("")
		}

		return nil
	}

	return filepath.Walk(source, sendFile)
}

func (c *cmdFile) recursiveMkdir(sftpConn *sftp.Client, p string, mode *os.FileMode, uid int64, gid int64) error {
	/* special case, every instance has a /, we don't need to do anything */
	if p == "/" {
		return nil
	}

	// Remove trailing "/" e.g. /A/B/C/. Otherwise we will end up with an
	// empty array entry "" which will confuse the Mkdir() loop below.
	pclean := filepath.Clean(p)
	parts := strings.Split(pclean, "/")
	i := len(parts)

	for ; i >= 1; i-- {
		cur := filepath.Join(parts[:i]...)
		fInfo, err := sftpConn.Lstat(cur)
		if err != nil {
			continue
		}

		if !fInfo.IsDir() {
			return fmt.Errorf(i18n.G("%s is not a directory"), cur)
		}

		i++
		break
	}

	for ; i <= len(parts); i++ {
		cur := filepath.Join(parts[:i]...)
		if cur == "" {
			continue
		}

		cur = "/" + cur

		modeArg := -1
		if mode != nil {
			modeArg = int(mode.Perm())
		}

		args := incus.InstanceFileArgs{
			UID:  uid,
			GID:  gid,
			Mode: modeArg,
			Type: "directory",
		}

		logger.Infof("Creating %s (%s)", cur, args.Type)
		err := c.sftpCreateFile(sftpConn, cur, args, false)
		if err != nil {
			return err
		}
	}

	return nil
}

// Mount.
type cmdFileMount struct {
	global *cmdGlobal
	file   *cmdFile

	flagListen   string
	flagAuthNone bool
	flagAuthUser string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdFileMount) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("mount", i18n.G("[<remote>:]<instance>[/<path>] [<target path>]"))
	cmd.Short = i18n.G("Mount files from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Mount files from instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus file mount foo/root fooroot
   To mount /root from the instance foo onto the local fooroot directory.`))

	cmd.Flags().StringVar(&c.flagListen, "listen", "", i18n.G("Setup SSH SFTP listener on address:port instead of mounting"))
	cmd.Flags().BoolVar(&c.flagAuthNone, "no-auth", false, i18n.G("Disable authentication when using SSH SFTP listener"))
	cmd.Flags().StringVar(&c.flagAuthUser, "auth-user", "", i18n.G("Set authentication user when using SSH SFTP listener"))

	cmd.RunE = c.Run

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

// Run runs the actual command logic.
func (c *cmdFileMount) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	var targetPath string

	// Determine the target if specified.
	if len(args) >= 2 {
		targetPath = filepath.Clean(args[len(args)-1])
		sb, err := os.Stat(targetPath)
		if err != nil {
			return err
		}

		if !sb.IsDir() {
			return errors.New(i18n.G("Target path must be a directory"))
		}
	}

	// Check which mode we should operate in. If target path is provided we use sshfs mode.
	if targetPath != "" && c.flagListen != "" {
		return errors.New(i18n.G("Target path and --listen flag cannot be used together"))
	}

	instSpec := strings.SplitN(resource.name, "/", 2)

	// Check instance path is provided in sshfs mode.
	if len(instSpec) < 2 && targetPath != "" {
		return fmt.Errorf(i18n.G("Invalid instance path: %q"), resource.name)
	}

	// Check instance path isn't provided in listener mode.
	if len(instSpec) > 1 && targetPath == "" {
		return errors.New(i18n.G("Instance path cannot be used in SSH SFTP listener mode"))
	}

	instName := instSpec[0]

	// Look for sshfs command if no SSH SFTP listener mode specified and a target mount path was specified.
	if c.flagListen == "" && targetPath != "" {
		// Setup sourcePath with leading / to ensure we reference the instance path from / location.
		instPath := instSpec[1]
		if instPath[0] != '/' {
			instPath = "/" + instPath
		}

		// Connect to SFTP.
		sftpConn, err := resource.server.GetInstanceFileSFTPConn(instName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
		}

		defer func() { _ = sftpConn.Close() }()

		return sshfsMount(cmd.Context(), sftpConn, instName, instPath, targetPath)
	}

	return sshSFTPServer(cmd.Context(), func() (net.Conn, error) { return resource.server.GetInstanceFileSFTPConn(instName) }, instName, c.flagAuthNone, c.flagAuthUser, c.flagListen)
}
