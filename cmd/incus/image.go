package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/termios"
	"github.com/lxc/incus/v6/shared/util"
)

type imageColumn struct {
	Name string
	Data func(api.Image) string
}

type cmdImage struct {
	global *cmdGlobal
}

func (c *cmdImage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("image")
	cmd.Short = i18n.G("Manage images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage images

Instances are created from images. Those images were themselves
either generated from an existing instance or downloaded from an image
server.

When using remote images, the server will automatically cache images for you
and remove them upon expiration.

The image unique identifier is the hash (sha-256) of its representation
as a compressed tarball (or for split images, the concatenation of the
metadata and rootfs tarballs).

Images can be referenced by their full hash, shortest unique partial
hash or alias name (if one is set).`))

	// Alias
	imageAliasCmd := cmdImageAlias{global: c.global, image: c}
	cmd.AddCommand(imageAliasCmd.Command())

	// Copy
	imageCopyCmd := cmdImageCopy{global: c.global, image: c}
	cmd.AddCommand(imageCopyCmd.Command())

	// Delete
	imageDeleteCmd := cmdImageDelete{global: c.global, image: c}
	cmd.AddCommand(imageDeleteCmd.Command())

	// Edit
	imageEditCmd := cmdImageEdit{global: c.global, image: c}
	cmd.AddCommand(imageEditCmd.Command())

	// Export
	imageExportCmd := cmdImageExport{global: c.global, image: c}
	cmd.AddCommand(imageExportCmd.Command())

	// Import
	imageImportCmd := cmdImageImport{global: c.global, image: c}
	cmd.AddCommand(imageImportCmd.Command())

	// Info
	imageInfoCmd := cmdImageInfo{global: c.global, image: c}
	cmd.AddCommand(imageInfoCmd.Command())

	// List
	imageListCmd := cmdImageList{global: c.global, image: c}
	cmd.AddCommand(imageListCmd.Command())

	// Refresh
	imageRefreshCmd := cmdImageRefresh{global: c.global, image: c}
	cmd.AddCommand(imageRefreshCmd.Command())

	// Show
	imageShowCmd := cmdImageShow{global: c.global, image: c}
	cmd.AddCommand(imageShowCmd.Command())

	// Get-property
	imageGetPropCmd := cmdImageGetProp{global: c.global, image: c}
	cmd.AddCommand(imageGetPropCmd.Command())

	// Set-property
	imageSetPropCmd := cmdImageSetProp{global: c.global, image: c}
	cmd.AddCommand(imageSetPropCmd.Command())

	// Unset-property
	imageUnsetPropCmd := cmdImageUnsetProp{global: c.global, image: c, imageSetProp: &imageSetPropCmd}
	cmd.AddCommand(imageUnsetPropCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

func (c *cmdImage) dereferenceAlias(d incus.ImageServer, imageType string, inName string) string {
	if inName == "" {
		inName = "default"
	}

	result, _, _ := d.GetImageAliasType(imageType, inName)
	if result == nil {
		return inName
	}

	return result.Target
}

// Copy.
type cmdImageCopy struct {
	global *cmdGlobal
	image  *cmdImage

	flagAliases       []string
	flagPublic        bool
	flagCopyAliases   bool
	flagAutoUpdate    bool
	flagVM            bool
	flagMode          string
	flagTargetProject string
	flagProfile       []string
}

func (c *cmdImageCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<image> <remote>:"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy images between servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy images between servers

The auto-update flag instructs the server to keep this image up to date.
It requires the source to be an alias and for it to be public.`))

	cmd.Flags().BoolVar(&c.flagPublic, "public", false, i18n.G("Make image public"))
	cmd.Flags().BoolVar(&c.flagCopyAliases, "copy-aliases", false, i18n.G("Copy aliases from source"))
	cmd.Flags().BoolVar(&c.flagAutoUpdate, "auto-update", false, i18n.G("Keep the image up to date after initial copy"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New aliases to add to the image")+"``")
	cmd.Flags().BoolVar(&c.flagVM, "vm", false, i18n.G("Copy virtual machine images"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay")+"``")
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new image")+"``")
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageCopy) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	if c.flagMode != "pull" && c.flagAutoUpdate {
		return fmt.Errorf(i18n.G("Auto update is only available in pull mode"))
	}

	// Parse source remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	sourceServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Revert project for `sourceServer` which may have been overwritten
	// by `--project` flag in `GetImageServer` method
	remote := conf.Remotes[remoteName]
	if remote.Protocol != "simplestream" && !remote.Public {
		d, ok := sourceServer.(incus.InstanceServer)
		if ok {
			sourceServer = d.UseProject(remote.Project)
		}
	}

	// Parse destination remote
	resources, err := c.global.ParseServers(args[1])
	if err != nil {
		return err
	}

	destinationServer := resources[0].server

	if resources[0].name != "" {
		return fmt.Errorf(i18n.G("Can't provide a name for the target image"))
	}

	// Resolve image type
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	if c.flagTargetProject != "" {
		destinationServer = destinationServer.UseProject(c.flagTargetProject)
	}

	// Copy the image
	var imgInfo *api.Image
	var fp string
	if conf.Remotes[remoteName].Protocol == "simplestreams" && !c.flagCopyAliases && len(c.flagAliases) == 0 {
		// All simplestreams images are always public, so unless we
		// need the aliases list too or the real fingerprint, we can skip the otherwise very expensive
		// alias resolution and image info retrieval step.
		imgInfo = &api.Image{}
		imgInfo.Fingerprint = name
		imgInfo.Public = true
	} else {
		// Resolve any alias and then grab the image information from the source
		image := c.image.dereferenceAlias(sourceServer, imageType, name)
		imgInfo, _, err = sourceServer.GetImage(image)
		if err != nil {
			return err
		}

		// Store the fingerprint for use when creating aliases later (as imgInfo.Fingerprint may be overridden)
		fp = imgInfo.Fingerprint
	}

	if imgInfo.Public && imgInfo.Fingerprint != name && !strings.HasPrefix(imgInfo.Fingerprint, name) {
		// If dealing with an alias, set the imgInfo fingerprint to match the provided alias (needed for auto-update)
		imgInfo.Fingerprint = name
	}

	copyArgs := incus.ImageCopyArgs{
		AutoUpdate: c.flagAutoUpdate,
		Public:     c.flagPublic,
		Type:       imageType,
		Mode:       c.flagMode,
		Profiles:   c.flagProfile,
	}

	// Do the copy
	op, err := destinationServer.CopyImage(sourceServer, *imgInfo, &copyArgs)
	if err != nil {
		return err
	}

	// Register progress handler
	progress := cli.ProgressRenderer{
		Format: i18n.G("Copying the image: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done(i18n.G("Image copied successfully!"))

	// Ensure aliases
	aliases := make([]api.ImageAlias, len(c.flagAliases))
	for i, entry := range c.flagAliases {
		aliases[i].Name = entry
	}

	if c.flagCopyAliases {
		// Also add the original aliases
		aliases = append(aliases, imgInfo.Aliases...)
	}

	err = ensureImageAliases(destinationServer, aliases, fp)
	if err != nil {
		return err
	}

	return nil
}

// Delete.
type cmdImageDelete struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<image> [[<remote>:]<image>...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete images`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdImageDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	for _, resource := range resources {
		if resource.name == "" {
			return fmt.Errorf(i18n.G("Image identifier missing"))
		}

		image := c.image.dereferenceAlias(resource.server, "", resource.name)
		op, err := resource.server.DeleteImage(image)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

// Edit.
type cmdImageEdit struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<image>"))
	cmd.Short = i18n.G("Edit image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit image properties`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus image edit <image>
    Launch a text editor to edit the properties

incus image edit <image> < image.yaml
    Load the image properties from a YAML file`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the image properties.
### Any line starting with a '# will be ignored.
###
### Each property is represented by a single line:
### An example would be:
###  description: My custom image`)
}

func (c *cmdImageEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Image identifier missing: %s"), args[0])
	}

	// Resolve any aliases
	image := c.image.dereferenceAlias(resource.server, "", resource.name)
	if image == "" {
		image = resource.name
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ImagePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateImage(image, newdata, "")
	}

	// Extract the current value
	imgInfo, etag, err := resource.server.GetImage(image)
	if err != nil {
		return err
	}

	brief := imgInfo.Writable()
	data, err := yaml.Marshal(&brief)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ImagePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateImage(image, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Export.
type cmdImageExport struct {
	global *cmdGlobal
	image  *cmdImage

	flagVM bool
}

func (c *cmdImageExport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<remote>:]<image> [<target>]"))
	cmd.Short = i18n.G("Export and download images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export and download images

The output target is optional and defaults to the working directory.`))

	cmd.Flags().BoolVar(&c.flagVM, "vm", false, i18n.G("Query virtual machine images"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageExport) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Resolve aliases
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	fingerprint := c.image.dereferenceAlias(remoteServer, imageType, name)

	// Default target is current directory
	target := "."
	targetMeta := fingerprint
	if len(args) > 1 {
		target = args[1]
		if internalUtil.IsDir(args[1]) {
			targetMeta = filepath.Join(args[1], targetMeta)
		} else {
			targetMeta = args[1]
		}
	}
	targetRootfs := targetMeta + ".root"

	// Prepare the files
	dest, err := os.Create(targetMeta)
	if err != nil {
		return err
	}

	defer func() { _ = dest.Close() }()

	destRootfs, err := os.Create(targetRootfs)
	if err != nil {
		return err
	}

	defer func() { _ = destRootfs.Close() }()

	// Prepare the download request
	progress := cli.ProgressRenderer{
		Format: i18n.G("Exporting the image: %s"),
		Quiet:  c.global.flagQuiet,
	}

	req := incus.ImageFileRequest{
		MetaFile:        io.WriteSeeker(dest),
		RootfsFile:      io.WriteSeeker(destRootfs),
		ProgressHandler: progress.UpdateProgress,
	}

	// Download the image
	resp, err := remoteServer.GetImageFile(fingerprint, req)
	if err != nil {
		_ = os.Remove(targetMeta)
		_ = os.Remove(targetRootfs)
		progress.Done("")
		return err
	}

	// Truncate down to size
	if resp.RootfsSize > 0 {
		err = destRootfs.Truncate(resp.RootfsSize)
		if err != nil {
			return err
		}
	}

	err = dest.Truncate(resp.MetaSize)
	if err != nil {
		return err
	}

	// Cleanup
	if resp.RootfsSize == 0 {
		err := os.Remove(targetRootfs)
		if err != nil {
			_ = os.Remove(targetMeta)
			_ = os.Remove(targetRootfs)
			progress.Done("")
			return err
		}
	}

	// Rename files
	if internalUtil.IsDir(target) {
		if resp.MetaName != "" {
			err := os.Rename(targetMeta, filepath.Join(target, resp.MetaName))
			if err != nil {
				_ = os.Remove(targetMeta)
				_ = os.Remove(targetRootfs)
				progress.Done("")
				return err
			}
		}

		if resp.RootfsSize > 0 && resp.RootfsName != "" {
			err := os.Rename(targetRootfs, filepath.Join(target, resp.RootfsName))
			if err != nil {
				_ = os.Remove(targetMeta)
				_ = os.Remove(targetRootfs)
				progress.Done("")
				return err
			}
		}
	} else if resp.RootfsSize == 0 && len(args) > 1 {
		if resp.MetaName != "" {
			extension := strings.SplitN(resp.MetaName, ".", 2)[1]
			err := os.Rename(targetMeta, fmt.Sprintf("%s.%s", targetMeta, extension))
			if err != nil {
				_ = os.Remove(targetMeta)
				progress.Done("")
				return err
			}
		}
	}

	progress.Done(i18n.G("Image exported successfully!"))
	return nil
}

// Import.
type cmdImageImport struct {
	global *cmdGlobal
	image  *cmdImage

	flagPublic  bool
	flagAliases []string
}

func (c *cmdImageImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", i18n.G("<tarball>|<directory>|<URL> [<rootfs tarball>] [<remote>:] [key=value...]"))
	cmd.Short = i18n.G("Import images into the image store")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import image into the image store

Directory import is only available on Linux and must be performed as root.`))

	cmd.Flags().BoolVar(&c.flagPublic, "public", false, i18n.G("Make image public"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New aliases to add to the image")+"``")
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageImport) packImageDir(path string) (string, error) {
	// Quick checks.
	if os.Geteuid() == -1 {
		return "", fmt.Errorf(i18n.G("Directory import is not available on this platform"))
	} else if os.Geteuid() != 0 {
		return "", fmt.Errorf(i18n.G("Must run as root to import from directory"))
	}

	outFile, err := os.CreateTemp("", "incus_image_")
	if err != nil {
		return "", err
	}

	defer func() { _ = outFile.Close() }()

	outFileName := outFile.Name()
	_, err = subprocess.RunCommand("tar", "-C", path, "--numeric-owner", "--restrict", "--force-local", "--xattrs", "-cJf", outFileName, "rootfs", "templates", "metadata.yaml")
	if err != nil {
		return "", err
	}

	return outFileName, outFile.Close()
}

func (c *cmdImageImport) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Import the image
	var imageFile string
	var rootfsFile string
	var properties []string
	var remote string

	for _, arg := range args {
		split := strings.Split(arg, "=")
		if len(split) == 1 || util.PathExists(arg) {
			if strings.HasSuffix(arg, ":") {
				var err error
				remote, _, err = conf.ParseRemote(arg)
				if err != nil {
					return err
				}
			} else {
				if imageFile == "" {
					imageFile = args[0]
				} else {
					rootfsFile = arg
				}
			}
		} else {
			properties = append(properties, arg)
		}
	}

	if remote == "" {
		remote = conf.DefaultRemote
	}

	if imageFile == "" {
		imageFile = args[0]
	}

	if util.PathExists(filepath.Clean(imageFile)) {
		imageFile = filepath.Clean(imageFile)
	}

	if rootfsFile != "" && util.PathExists(filepath.Clean(rootfsFile)) {
		rootfsFile = filepath.Clean(rootfsFile)
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if strings.HasPrefix(imageFile, "http://") {
		return fmt.Errorf(i18n.G("Only https:// is supported for remote image import"))
	}

	var createArgs *incus.ImageCreateArgs
	image := api.ImagesPost{}
	image.Public = c.flagPublic

	// Handle properties
	for _, entry := range properties {
		fields := strings.SplitN(entry, "=", 2)
		if len(fields) < 2 {
			return fmt.Errorf(i18n.G("Bad property: %s"), entry)
		}

		if image.Properties == nil {
			image.Properties = map[string]string{}
		}

		image.Properties[strings.TrimSpace(fields[0])] = strings.TrimSpace(fields[1])
	}

	progress := cli.ProgressRenderer{
		Format: i18n.G("Transferring image: %s"),
		Quiet:  c.global.flagQuiet,
	}

	imageType := "container"
	if strings.HasPrefix(imageFile, "https://") {
		image.Source = &api.ImagesPostSource{}
		image.Source.Type = "url"
		image.Source.Mode = "pull"
		image.Source.Protocol = "direct"
		image.Source.URL = imageFile
		createArgs = nil
	} else {
		var meta io.ReadCloser
		var rootfs io.ReadCloser

		// Open meta
		if internalUtil.IsDir(imageFile) {
			imageFile, err = c.packImageDir(imageFile)
			if err != nil {
				return err
			}
			// remove temp file
			defer func() { _ = os.Remove(imageFile) }()
		}

		meta, err = os.Open(imageFile)
		if err != nil {
			return err
		}

		defer func() { _ = meta.Close() }()

		// Open rootfs
		if rootfsFile != "" {
			rootfs, err = os.Open(rootfsFile)
			if err != nil {
				return err
			}

			defer func() { _ = rootfs.Close() }()

			_, ext, _, err := archive.DetectCompressionFile(rootfs)
			if err != nil {
				return err
			}

			_, err = rootfs.(*os.File).Seek(0, io.SeekStart)
			if err != nil {
				return err
			}

			if ext == ".qcow2" {
				imageType = "virtual-machine"
			}
		}

		createArgs = &incus.ImageCreateArgs{
			MetaFile:        meta,
			MetaName:        filepath.Base(imageFile),
			RootfsFile:      rootfs,
			RootfsName:      filepath.Base(rootfsFile),
			ProgressHandler: progress.UpdateProgress,
			Type:            imageType,
		}

		image.Filename = createArgs.MetaName
	}

	// Start the transfer
	op, err := d.CreateImage(image, createArgs)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	opAPI := op.Get()

	// Get the fingerprint
	fingerprint := opAPI.Metadata["fingerprint"].(string)
	progress.Done(fmt.Sprintf(i18n.G("Image imported with fingerprint: %s"), fingerprint))

	// Add the aliases
	if len(c.flagAliases) > 0 {
		aliases := make([]api.ImageAlias, len(c.flagAliases))
		for i, entry := range c.flagAliases {
			aliases[i].Name = entry
		}

		err = ensureImageAliases(d, aliases, fingerprint)
		if err != nil {
			return err
		}
	}

	return nil
}

// Info.
type cmdImageInfo struct {
	global *cmdGlobal
	image  *cmdImage

	flagVM bool
}

func (c *cmdImageInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<image>"))
	cmd.Short = i18n.G("Show useful information about images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show useful information about images`))

	cmd.Flags().BoolVar(&c.flagVM, "vm", false, i18n.G("Query virtual machine images"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageInfo) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Render info
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	image := c.image.dereferenceAlias(remoteServer, imageType, name)
	info, _, err := remoteServer.GetImage(image)
	if err != nil {
		return err
	}

	public := i18n.G("no")
	if info.Public {
		public = i18n.G("yes")
	}

	cached := i18n.G("no")
	if info.Cached {
		cached = i18n.G("yes")
	}

	autoUpdate := i18n.G("disabled")
	if info.AutoUpdate {
		autoUpdate = i18n.G("enabled")
	}

	imgType := "container"
	if info.Type != "" {
		imgType = info.Type
	}

	fmt.Printf(i18n.G("Fingerprint: %s")+"\n", info.Fingerprint)
	fmt.Printf(i18n.G("Size: %.2fMiB")+"\n", float64(info.Size)/1024.0/1024.0)
	fmt.Printf(i18n.G("Architecture: %s")+"\n", info.Architecture)
	fmt.Printf(i18n.G("Type: %s")+"\n", imgType)
	fmt.Printf(i18n.G("Public: %s")+"\n", public)
	fmt.Printf(i18n.G("Timestamps:") + "\n")

	if !info.CreatedAt.IsZero() {
		fmt.Printf("    "+i18n.G("Created: %s")+"\n", info.CreatedAt.Local().Format(dateLayout))
	}

	fmt.Printf("    "+i18n.G("Uploaded: %s")+"\n", info.UploadedAt.Local().Format(dateLayout))

	if !info.ExpiresAt.IsZero() {
		fmt.Printf("    "+i18n.G("Expires: %s")+"\n", info.ExpiresAt.Local().Format(dateLayout))
	} else {
		fmt.Printf("    " + i18n.G("Expires: never") + "\n")
	}

	if !info.LastUsedAt.IsZero() {
		fmt.Printf("    "+i18n.G("Last used: %s")+"\n", info.LastUsedAt.Local().Format(dateLayout))
	} else {
		fmt.Printf("    " + i18n.G("Last used: never") + "\n")
	}

	fmt.Println(i18n.G("Properties:"))
	for key, value := range info.Properties {
		fmt.Printf("    %s: %s\n", key, value)
	}

	fmt.Println(i18n.G("Aliases:"))
	for _, alias := range info.Aliases {
		if alias.Description != "" {
			fmt.Printf("    - %s (%s)\n", alias.Name, alias.Description)
		} else {
			fmt.Printf("    - %s\n", alias.Name)
		}
	}

	fmt.Printf(i18n.G("Cached: %s")+"\n", cached)
	fmt.Printf(i18n.G("Auto update: %s")+"\n", autoUpdate)

	if info.UpdateSource != nil {
		fmt.Println(i18n.G("Source:"))
		fmt.Printf("    "+i18n.G("Server: %s")+"\n", info.UpdateSource.Server)
		fmt.Printf("    "+i18n.G("Protocol: %s")+"\n", info.UpdateSource.Protocol)
		fmt.Printf("    "+i18n.G("Alias: %s")+"\n", info.UpdateSource.Alias)
	}

	if len(info.Profiles) == 0 {
		fmt.Printf(i18n.G("Profiles: ") + "[]\n")
	} else {
		fmt.Println(i18n.G("Profiles:"))
		for _, name := range info.Profiles {
			fmt.Printf("    - %s\n", name)
		}
	}

	return nil
}

// List.
type cmdImageList struct {
	global *cmdGlobal
	image  *cmdImage

	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

func (c *cmdImageList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:] [<filter>...]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List images

Filters may be of the <key>=<value> form for property based filtering,
or part of the image hash or part of the image alias name.

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Default column layout is: lfpdasu

Column shorthand chars:

    l - Shortest image alias (and optionally number of other aliases)
    L - Newline-separated list of all image aliases
    f - Fingerprint (short)
    F - Fingerprint (long)
    p - Whether image is public
    d - Description
    e - Project
    a - Architecture
    s - Size
    u - Upload date
    t - Type`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultImagesColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display images from all projects"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

const defaultImagesColumns = "lfpdatsu"
const defaultImagesColumnsAllProjects = "elfpdatsu"

func (c *cmdImageList) parseColumns() ([]imageColumn, error) {
	columnsShorthandMap := map[rune]imageColumn{
		'a': {i18n.G("ARCHITECTURE"), c.architectureColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'e': {i18n.G("PROJECT"), c.projectColumnData},
		'f': {i18n.G("FINGERPRINT"), c.fingerprintColumnData},
		'F': {i18n.G("FINGERPRINT"), c.fingerprintFullColumnData},
		'l': {i18n.G("ALIAS"), c.aliasColumnData},
		'L': {i18n.G("ALIASES"), c.aliasesColumnData},
		'p': {i18n.G("PUBLIC"), c.publicColumnData},
		's': {i18n.G("SIZE"), c.sizeColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'u': {i18n.G("UPLOAD DATE"), c.uploadDateColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []imageColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if ok {
				columns = append(columns, column)
			} else {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}
		}
	}

	return columns, nil
}

func (c *cmdImageList) aliasColumnData(image api.Image) string {
	shortest := c.shortestAlias(image.Aliases)
	if len(image.Aliases) > 1 {
		shortest = fmt.Sprintf(i18n.G("%s (%d more)"), shortest, len(image.Aliases)-1)
	}

	return shortest
}

func (c *cmdImageList) aliasesColumnData(image api.Image) string {
	aliases := []string{}
	for _, alias := range image.Aliases {
		aliases = append(aliases, alias.Name)
	}

	sort.Strings(aliases)
	return strings.Join(aliases, "\n")
}

func (c *cmdImageList) fingerprintColumnData(image api.Image) string {
	return image.Fingerprint[0:12]
}

func (c *cmdImageList) fingerprintFullColumnData(image api.Image) string {
	return image.Fingerprint
}

func (c *cmdImageList) publicColumnData(image api.Image) string {
	if image.Public {
		return i18n.G("yes")
	}

	return i18n.G("no")
}

func (c *cmdImageList) descriptionColumnData(image api.Image) string {
	return c.findDescription(image.Properties)
}

func (c *cmdImageList) projectColumnData(image api.Image) string {
	return image.Project
}

func (c *cmdImageList) architectureColumnData(image api.Image) string {
	return image.Architecture
}

func (c *cmdImageList) sizeColumnData(image api.Image) string {
	return fmt.Sprintf("%.2fMiB", float64(image.Size)/1024.0/1024.0)
}

func (c *cmdImageList) typeColumnData(image api.Image) string {
	if image.Type == "" {
		return "CONTAINER"
	}

	return strings.ToUpper(image.Type)
}

func (c *cmdImageList) uploadDateColumnData(image api.Image) string {
	return image.UploadedAt.Local().Format(dateLayout)
}

func (c *cmdImageList) shortestAlias(list []api.ImageAlias) string {
	shortest := ""
	for _, l := range list {
		if shortest == "" {
			shortest = l.Name
			continue
		}

		if len(l.Name) != 0 && len(l.Name) < len(shortest) {
			shortest = l.Name
		}
	}

	return shortest
}

func (c *cmdImageList) findDescription(props map[string]string) string {
	for k, v := range props {
		if k == "description" {
			return v
		}
	}
	return ""
}

func (c *cmdImageList) imageShouldShow(filters []string, state *api.Image) bool {
	if len(filters) == 0 {
		return true
	}

	m := structToMap(state)

	for _, filter := range filters {
		found := false
		if strings.Contains(filter, "=") {
			membs := strings.SplitN(filter, "=", 2)

			key := membs[0]
			var value string
			if len(membs) < 2 {
				value = ""
			} else {
				value = membs[1]
			}

			for configKey, configValue := range state.Properties {
				list := cmdList{}
				list.global = c.global
				if list.dotPrefixMatch(key, configKey) {
					//try to test filter value as a regexp
					regexpValue := value
					if !(strings.Contains(value, "^") || strings.Contains(value, "$")) {
						regexpValue = "^" + regexpValue + "$"
					}

					r, err := regexp.Compile(regexpValue)
					//if not regexp compatible use original value
					if err != nil {
						if value == configValue {
							found = true
							break
						}
					} else if r.MatchString(configValue) {
						found = true
						break
					}
				}
			}

			val, ok := m[key]
			if ok && fmt.Sprintf("%v", val) == value {
				found = true
			}
		} else {
			for _, alias := range state.Aliases {
				if strings.Contains(alias.Name, filter) {
					found = true
					break
				}
			}
			if strings.Contains(state.Fingerprint, filter) {
				found = true
			}
		}

		if !found {
			return false
		}
	}

	return true
}

func (c *cmdImageList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	// Add project column if --all-projects flag specified and no -c was passed.
	if c.flagAllProjects && c.flagColumns == defaultImagesColumns {
		c.flagColumns = defaultImagesColumnsAllProjects
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, name, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Process the filters
	filters := []string{}
	if name != "" {
		filters = append(filters, name)
	}

	if len(args) > 1 {
		filters = append(filters, args[1:]...)
	}

	// Process the columns
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	serverFilters, clientFilters := getServerSupportedFilters(filters, api.Image{})

	var allImages, images []api.Image
	if c.flagAllProjects {
		allImages, err = remoteServer.GetImagesAllProjects()
		if err != nil {
			return err
		}
	} else {
		allImages, err = remoteServer.GetImagesWithFilter(serverFilters)
		if err != nil {
			allImages, err = remoteServer.GetImages()
			if err != nil {
				return err
			}

			clientFilters = filters
		}
	}

	for _, image := range allImages {
		if !c.imageShouldShow(clientFilters, &image) {
			continue
		}

		images = append(images, image)
	}

	// Render the table
	data := [][]string{}
	for _, image := range images {
		if !c.imageShouldShow(clientFilters, &image) {
			continue
		}

		row := []string{}
		for _, column := range columns {
			row = append(row, column.Data(image))
		}

		data = append(data, row)
	}

	sort.Sort(cli.StringList(data))

	rawData := make([]*api.Image, len(images))
	for i := range images {
		rawData[i] = &images[i]
	}

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(c.flagFormat, headers, data, rawData)
}

// Refresh.
type cmdImageRefresh struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageRefresh) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("refresh", i18n.G("[<remote>:]<image> [[<remote>:]<image>...]"))
	cmd.Short = i18n.G("Refresh images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Refresh images`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdImageRefresh) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	for _, resource := range resources {
		if resource.name == "" {
			return fmt.Errorf(i18n.G("Image identifier missing"))
		}

		image := c.image.dereferenceAlias(resource.server, "", resource.name)
		progress := cli.ProgressRenderer{
			Format: i18n.G("Refreshing the image: %s"),
			Quiet:  c.global.flagQuiet,
		}

		op, err := resource.server.RefreshImage(image)
		if err != nil {
			return err
		}

		// Register progress handler
		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			return err
		}

		// Wait for the refresh to happen
		err = op.Wait()
		if err != nil {
			return err
		}

		opAPI := op.Get()

		// Check if refreshed
		refreshed := false
		flag, ok := opAPI.Metadata["refreshed"]
		if ok {
			refreshed = flag.(bool)
		}

		if refreshed {
			progress.Done(i18n.G("Image refreshed successfully!"))
		} else {
			progress.Done(i18n.G("Image already up to date."))
		}
	}

	return nil
}

// Show.
type cmdImageShow struct {
	global *cmdGlobal
	image  *cmdImage

	flagVM bool
}

func (c *cmdImageShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<image>"))
	cmd.Short = i18n.G("Show image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show image properties`))

	cmd.Flags().BoolVar(&c.flagVM, "vm", false, i18n.G("Query virtual machine images"))
	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Show properties
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	image := c.image.dereferenceAlias(remoteServer, imageType, name)
	info, _, err := remoteServer.GetImage(image)
	if err != nil {
		return err
	}

	properties := info.Writable()
	data, err := yaml.Marshal(&properties)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

type cmdImageGetProp struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageGetProp) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get-property", i18n.G("[<remote>:]<image> <key>"))
	cmd.Short = i18n.G("Get image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get image properties`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		if len(args) == 1 {
			// individual image prop could complete here
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageGetProp) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Get the corresponding property
	image := c.image.dereferenceAlias(remoteServer, "", name)
	info, _, err := remoteServer.GetImage(image)
	if err != nil {
		return err
	}

	prop, propFound := info.Properties[args[1]]
	if !propFound {
		return fmt.Errorf(i18n.G("Property not found"))
	}

	fmt.Println(prop)

	return nil
}

type cmdImageSetProp struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageSetProp) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set-property", i18n.G("[<remote>:]<image> <key> <value>"))
	cmd.Short = i18n.G("Set image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set image properties`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageSetProp) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Image identifier missing: %s"), args[0])
	}

	// Show properties
	image := c.image.dereferenceAlias(resource.server, "", resource.name)
	info, etag, err := resource.server.GetImage(image)
	if err != nil {
		return err
	}

	properties := info.Writable()
	properties.Properties[args[1]] = args[2]

	// Update image
	err = resource.server.UpdateImage(image, properties, etag)
	if err != nil {
		return err
	}

	return nil
}

type cmdImageUnsetProp struct {
	global       *cmdGlobal
	image        *cmdImage
	imageSetProp *cmdImageSetProp
}

func (c *cmdImageUnsetProp) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset-property", i18n.G("[<remote>:]<image> <key>"))
	cmd.Short = i18n.G("Unset image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset image properties`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageUnsetProp) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.imageSetProp.Run(cmd, args)
}

func structToMap(data any) map[string]any {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	mapData := make(map[string]any)

	err = json.Unmarshal(dataBytes, &mapData)
	if err != nil {
		return nil
	}

	return mapData
}
