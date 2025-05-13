package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"slices"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/termios"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// Date layout to be used throughout the client.
const dateLayout = "2006/01/02 15:04 MST"

// Batch operations.
type batchResult struct {
	err  error
	name string
}

func runBatch(names []string, action func(name string) error) []batchResult {
	chResult := make(chan batchResult, len(names))

	for _, name := range names {
		go func(name string) {
			chResult <- batchResult{action(name), name}
		}(name)
	}

	results := []batchResult{}
	for range names {
		results = append(results, <-chResult)
	}

	return results
}

// Add a device to an instance.
func instanceDeviceAdd(client incus.InstanceServer, name string, devName string, dev map[string]string) error {
	// Get the instance entry
	inst, etag, err := client.GetInstance(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := inst.Devices[devName]
	if ok {
		return fmt.Errorf(i18n.G("Device already exists: %s"), devName)
	}

	inst.Devices[devName] = dev

	op, err := client.UpdateInstance(name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Add a device to a profile.
func profileDeviceAdd(client incus.InstanceServer, name string, devName string, dev map[string]string) error {
	// Get the profile entry
	profile, profileEtag, err := client.GetProfile(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := profile.Devices[devName]
	if ok {
		return fmt.Errorf(i18n.G("Device already exists: %s"), devName)
	}

	// Add the device to the instance
	profile.Devices[devName] = dev

	err = client.UpdateProfile(name, profile.Writable(), profileEtag)
	if err != nil {
		return err
	}

	return nil
}

// parseDeviceOverrides parses device overrides of the form "<deviceName>,<key>=<value>" into a device map.
// The resulting device map is unlikely to contain valid devices as these are simply values to be overridden.
func parseDeviceOverrides(deviceOverrideArgs []string) (map[string]map[string]string, error) {
	deviceMap := map[string]map[string]string{}
	for _, entry := range deviceOverrideArgs {
		if !strings.Contains(entry, "=") || !strings.Contains(entry, ",") {
			return nil, fmt.Errorf(i18n.G("Bad device override syntax, expecting <device>,<key>=<value>: %s"), entry)
		}

		deviceFields := strings.SplitN(entry, ",", 2)
		keyFields := strings.SplitN(deviceFields[1], "=", 2)

		if deviceMap[deviceFields[0]] == nil {
			deviceMap[deviceFields[0]] = map[string]string{}
		}

		deviceMap[deviceFields[0]][keyFields[0]] = keyFields[1]
	}

	return deviceMap, nil
}

// IsAliasesSubset returns true if the first array is completely contained in the second array.
func IsAliasesSubset(a1 []api.ImageAlias, a2 []api.ImageAlias) bool {
	set := make(map[string]any)
	for _, alias := range a2 {
		set[alias.Name] = nil
	}

	for _, alias := range a1 {
		_, found := set[alias.Name]
		if !found {
			return false
		}
	}

	return true
}

// GetCommonAliases returns the common aliases between a list of aliases and all the existing ones.
func GetCommonAliases(client incus.InstanceServer, aliases ...api.ImageAlias) ([]api.ImageAliasesEntry, error) {
	if len(aliases) == 0 {
		return nil, nil
	}

	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}

	// 'GetExistingAliases' which is using 'sort.SearchStrings' requires sorted slice
	sort.Strings(names)

	resp, err := client.GetImageAliases()
	if err != nil {
		return nil, err
	}

	return GetExistingAliases(names, resp), nil
}

// Create the specified image aliases, updating those that already exist.
func ensureImageAliases(client incus.InstanceServer, aliases []api.ImageAlias, fingerprint string) error {
	if len(aliases) == 0 {
		return nil
	}

	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}

	sort.Strings(names)

	resp, err := client.GetImageAliases()
	if err != nil {
		return err
	}

	// Delete existing aliases that match provided ones
	for _, alias := range GetExistingAliases(names, resp) {
		err := client.DeleteImageAlias(alias.Name)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to remove alias %s: %w"), alias.Name, err)
		}
	}

	// Create new aliases.
	for _, alias := range aliases {
		aliasPost := api.ImageAliasesPost{}
		aliasPost.Name = alias.Name
		aliasPost.Target = fingerprint
		err := client.CreateImageAlias(aliasPost)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to create alias %s: %w"), alias.Name, err)
		}
	}

	return nil
}

// GetExistingAliases returns the intersection between a list of aliases and all the existing ones.
func GetExistingAliases(aliases []string, allAliases []api.ImageAliasesEntry) []api.ImageAliasesEntry {
	existing := []api.ImageAliasesEntry{}
	for _, alias := range allAliases {
		name := alias.Name
		pos := sort.SearchStrings(aliases, name)
		if pos < len(aliases) && aliases[pos] == name {
			existing = append(existing, alias)
		}
	}
	return existing
}

// deleteImagesByAliases deletes images based on provided aliases. E.g.
// aliases=[a1], image aliases=[a1] - image will be deleted
// aliases=[a1, a2], image aliases=[a1] - image will be deleted
// aliases=[a1], image aliases=[a1, a2] - image will be preserved.
func deleteImagesByAliases(client incus.InstanceServer, aliases []api.ImageAlias) error {
	existingAliases, err := GetCommonAliases(client, aliases...)
	if err != nil {
		return fmt.Errorf(i18n.G("Error retrieving aliases: %w"), err)
	}

	// Nothing to do. Just return.
	if len(existingAliases) == 0 {
		return nil
	}

	// Delete images if necessary
	visitedImages := make(map[string]any)
	for _, alias := range existingAliases {
		image, _, _ := client.GetImage(alias.Target)

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
		//    with aliases 'foo' and 'bar'. Old image should be removed.
		// 2. If image with 'foo' and 'bar' aliases already exists and new image is published
		//    with alias 'foo'. Old image should be kept with alias 'bar'
		//    and new image will have 'foo' alias.
		if image != nil && IsAliasesSubset(image.Aliases, aliases) {
			op, err := client.DeleteImage(alias.Target)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func getConfig(args ...string) (map[string]string, error) {
	if len(args) == 2 && !strings.Contains(args[0], "=") {
		if args[1] == "-" && !termios.IsTerminal(getStdinFd()) {
			buf, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf(i18n.G("Can't read from stdin: %w"), err)
			}

			args[1] = string(buf[:])
		}

		return map[string]string{args[0]: args[1]}, nil
	}

	values := map[string]string{}

	for _, arg := range args {
		fields := strings.SplitN(arg, "=", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf(i18n.G("Invalid key=value configuration: %s"), arg)
		}

		if fields[1] == "-" && !termios.IsTerminal(getStdinFd()) {
			buf, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf(i18n.G("Can't read from stdin: %w"), err)
			}

			fields[1] = string(buf[:])
		}

		values[fields[0]] = fields[1]
	}

	return values, nil
}

func readEnvironmentFile(path string) (map[string]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf(i18n.G("Can't read from environment file: %w"), err)
	}

	// Split the file into lines.
	lines := strings.Split(string(content), "\n")

	// Create a map to store the key value pairs.
	envMap := make(map[string]string)

	// Iterate over the lines.
	for _, line := range lines {
		if line == "" {
			continue
		}

		pieces := strings.SplitN(line, "=", 2)
		value := ""
		if len(pieces) > 1 {
			value = pieces[1]
		}

		envMap[pieces[0]] = value
	}

	return envMap, nil
}

func usage(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}

	return name + " " + args[0]
}

// instancesExist iterates over a list of instances (or snapshots) and checks that they exist.
func instancesExist(resources []remoteResource) error {
	for _, resource := range resources {
		// Handle snapshots.
		if instance.IsSnapshot(resource.name) {
			parent, snap, _ := api.GetParentAndSnapshotName(resource.name)

			_, _, err := resource.server.GetInstanceSnapshot(parent, snap)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed checking instance snapshot exists \"%s:%s\": %w"), resource.remote, resource.name, err)
			}

			continue
		}

		_, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed checking instance exists \"%s:%s\": %w"), resource.remote, resource.name, err)
		}
	}

	return nil
}

// structHasField checks if specified struct includes field with given name.
func structHasField(typ reflect.Type, field string) bool {
	var parent reflect.Type

	for i := range typ.NumField() {
		fieldType := typ.Field(i)
		yaml := fieldType.Tag.Get("yaml")

		if yaml == ",inline" {
			parent = fieldType.Type
		}

		if yaml == field {
			return true
		}
	}

	if parent != nil {
		return structHasField(parent, field)
	}

	return false
}

// getServerSupportedFilters returns two lists: one with filters supported by server and second one with not supported.
func getServerSupportedFilters(filters []string, clientFilters []string, singleValueServerSupport bool) ([]string, []string) {
	supportedFilters := []string{}
	unsupportedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)

		if len(membs) == 1 && singleValueServerSupport {
			supportedFilters = append(supportedFilters, filter)
			continue
		} else if len(membs) == 1 && !singleValueServerSupport {
			unsupportedFilters = append(unsupportedFilters, filter)
			continue
		}

		found := false
		if slices.Contains(clientFilters, membs[0]) {
			found = true
			unsupportedFilters = append(unsupportedFilters, filter)
		}

		if found {
			continue
		}

		supportedFilters = append(supportedFilters, filter)
	}

	return supportedFilters, unsupportedFilters
}

// guessImage checks that the image name (provided by the user) is correct given an instance remote and image remote.
func guessImage(conf *config.Config, d incus.InstanceServer, instRemote string, imgRemote string, imageRef string) (string, string) {
	if instRemote != imgRemote {
		return imgRemote, imageRef
	}

	fields := strings.SplitN(imageRef, "/", 2)
	_, ok := conf.Remotes[fields[0]]
	if !ok {
		return imgRemote, imageRef
	}

	_, _, err := d.GetImageAlias(imageRef)
	if err == nil {
		return imgRemote, imageRef
	}

	_, _, err = d.GetImage(imageRef)
	if err == nil {
		return imgRemote, imageRef
	}

	if len(fields) == 1 {
		fmt.Fprintf(os.Stderr, i18n.G("The local image '%q' couldn't be found, trying '%q:' instead.")+"\n", imageRef, fields[0])
		return fields[0], "default"
	}

	fmt.Fprintf(os.Stderr, i18n.G("The local image '%q' couldn't be found, trying '%q:%q' instead.")+"\n", imageRef, fields[0], fields[1])
	return fields[0], fields[1]
}

// getImgInfo returns an image server and image info for the given image name (given by a user)
// an image remote and an instance remote.
func getImgInfo(d incus.InstanceServer, conf *config.Config, imgRemote string, instRemote string, imageRef string, source *api.InstanceSource) (incus.ImageServer, *api.Image, error) {
	var imgRemoteServer incus.ImageServer
	var imgInfo *api.Image
	var err error

	// Connect to the image server
	if imgRemote == instRemote {
		imgRemoteServer = d
	} else {
		imgRemoteServer, err = conf.GetImageServer(imgRemote)
		if err != nil {
			return nil, nil, err
		}
	}

	// Optimisation for public image servers.
	if conf.Remotes[imgRemote].Protocol != "incus" {
		imgInfo = &api.Image{}
		imgInfo.Fingerprint = imageRef
		imgInfo.Public = true
		source.Alias = imageRef
	} else {
		// Attempt to resolve an image alias
		alias, _, err := imgRemoteServer.GetImageAlias(imageRef)
		if err == nil {
			source.Alias = imageRef
			imageRef = alias.Target
		}

		// Get the image info
		imgInfo, _, err = imgRemoteServer.GetImage(imageRef)
		if err != nil {
			return nil, nil, err
		}
	}

	return imgRemoteServer, imgInfo, nil
}

// Spawn the editor with a temporary YAML file for editing configs.
func textEditor(inPath string, inContent []byte) ([]byte, error) {
	var f *os.File
	var err error
	var path string

	// Detect the text editor to use
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			for _, p := range []string{"editor", "vi", "emacs", "nano"} {
				_, err := exec.LookPath(p)
				if err == nil {
					editor = p
					break
				}
			}
			if editor == "" {
				return []byte{}, errors.New(i18n.G("No text editor found, please set the EDITOR environment variable"))
			}
		}
	}

	if inPath == "" {
		// If provided input, create a new file
		f, err = os.CreateTemp("", "incus_editor_")
		if err != nil {
			return []byte{}, err
		}

		reverter := revert.New()
		defer reverter.Fail()

		reverter.Add(func() {
			_ = f.Close()
			_ = os.Remove(f.Name())
		})

		err = os.Chmod(f.Name(), 0o600)
		if err != nil {
			return []byte{}, err
		}

		_, err = f.Write(inContent)
		if err != nil {
			return []byte{}, err
		}

		err = f.Close()
		if err != nil {
			return []byte{}, err
		}

		path = fmt.Sprintf("%s.yaml", f.Name())
		err = os.Rename(f.Name(), path)
		if err != nil {
			return []byte{}, err
		}

		reverter.Success()
		reverter.Add(func() { _ = os.Remove(path) })
	} else {
		path = inPath
	}

	cmdParts := strings.Fields(editor)
	cmd := exec.Command(cmdParts[0], append(cmdParts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return []byte{}, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
}

// removeElementsFromSlice returns a slice equivalent to removing the given elements from the given list.
// Elements not present in the list are ignored.
func removeElementsFromSlice[T comparable](list []T, elements ...T) []T {
	for i := len(elements) - 1; i >= 0; i-- {
		element := elements[i]
		match := false
		for j := len(list) - 1; j >= 0; j-- {
			if element == list[j] {
				match = true
				list = slices.Delete(list, j, j+1)
				break
			}
		}

		if match {
			elements = slices.Delete(elements, i, i+1)
		}
	}

	return list
}

// sshfsMount mounts the instance's filesystem using sshfs by piping the instance's SFTP connection to sshfs.
func sshfsMount(ctx context.Context, sftpConn net.Conn, entity string, relPath string, targetPath string) error {
	// Use the format "incus.<instance_name>" as the source "host" (although not used for communication)
	// so that the mount can be seen to be associated with Incus and the instance in the local mount table.
	sourceURL := fmt.Sprintf("incus.%s:%s", entity, relPath)

	sshfsCmd := exec.Command("sshfs", "-o", "slave", sourceURL, targetPath)

	// Setup pipes.
	stdin, err := sshfsCmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := sshfsCmd.StdoutPipe()
	if err != nil {
		return err
	}

	sshfsCmd.Stderr = os.Stderr

	err = sshfsCmd.Start()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed starting sshfs: %w"), err)
	}

	fmt.Printf(i18n.G("sshfs mounting %q on %q")+"\n", fmt.Sprintf("%s%s", entity, relPath), targetPath)
	fmt.Println(i18n.G("Press ctrl+c to finish"))

	ctx, cancel := context.WithCancel(ctx)
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)
	go func() {
		select {
		case <-chSignal:
		case <-ctx.Done():
		}

		cancel()                                  // Prevents error output when the io.Copy functions finish.
		_ = sshfsCmd.Process.Signal(os.Interrupt) // This will cause sshfs to unmount.
		_ = stdin.Close()
	}()

	go func() {
		_, err := io.Copy(stdin, sftpConn)
		if ctx.Err() == nil {
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("I/O copy from instance to sshfs failed: %v")+"\n", err)
			} else {
				fmt.Println(i18n.G("Instance disconnected"))
			}
		}
		cancel() // Ask sshfs to end.
	}()

	_, err = io.Copy(sftpConn, stdout)
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, i18n.G("I/O copy from sshfs to instance failed: %v")+"\n", err)
	}

	cancel() // Ask sshfs to end.

	err = sshfsCmd.Wait()
	if err != nil {
		return err
	}

	fmt.Println(i18n.G("sshfs has stopped"))

	return sftpConn.Close()
}

// sshSFTPServer runs an SSH server listening on a random port of 127.0.0.1.
// It provides an unauthenticated SFTP server connected to the instance's filesystem.
func sshSFTPServer(ctx context.Context, sftpConn func() (net.Conn, error), entity string, authNone bool, authUser string, listenAddr string) error {
	randString := func(length int) string {
		chars := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0987654321")
		randStr := make([]rune, length)
		for i := range randStr {
			randStr[i] = chars[rand.Intn(len(chars))]
		}

		return string(randStr)
	}

	// Setup an SSH SFTP server.
	sshConfig := &ssh.ServerConfig{}

	var authPass string

	if authNone {
		sshConfig.NoClientAuth = true
	} else {
		if authUser == "" {
			authUser = randString(8)
		}

		authPass = randString(8)
		sshConfig.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == authUser && string(pass) == authPass {
				return nil, nil
			}

			return nil, fmt.Errorf(i18n.G("Password rejected for %q"), c.User())
		}
	}

	// Generate random host key.
	_, privKey, err := localtls.GenerateMemCert(false, false)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed generating SSH host key: %w"), err)
	}

	private, err := ssh.ParsePrivateKey(privKey)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed parsing SSH host key: %w"), err)
	}

	sshConfig.AddHostKey(private)

	if listenAddr == "" {
		listenAddr = "127.0.0.1:0" // Listen on a random local port if not specified.
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to listen for connection: %w"), err)
	}

	fmt.Printf(i18n.G("SSH SFTP listening on %v")+"\n", listener.Addr())

	if sshConfig.PasswordCallback != nil {
		fmt.Printf(i18n.G("Login with username %q and password %q")+"\n", authUser, authPass)
	} else {
		fmt.Println(i18n.G("Login without username and password"))
	}

	for {
		// Wait for new SSH connections.
		nConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to accept incoming connection: %w"), err)
		}

		// Handle each SSH connection in its own go routine.
		go func() {
			fmt.Printf(i18n.G("SSH client connected %q")+"\n", nConn.RemoteAddr())
			defer fmt.Printf(i18n.G("SSH client disconnected %q")+"\n", nConn.RemoteAddr())
			defer func() { _ = nConn.Close() }()

			// Before use, a handshake must be performed on the incoming net.Conn.
			_, chans, reqs, err := ssh.NewServerConn(nConn, sshConfig)
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Failed SSH handshake with client %q: %v")+"\n", nConn.RemoteAddr(), err)
				return
			}

			// The incoming Request channel must be serviced.
			go ssh.DiscardRequests(reqs)

			// Service the incoming Channel requests.
			for newChannel := range chans {
				localChannel := newChannel

				// Channels have a type, depending on the application level protocol intended.
				// In the case of an SFTP session, this is "subsystem" with a payload string of
				// "<length=4>sftp"
				if localChannel.ChannelType() != "session" {
					_ = localChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
					fmt.Fprintf(os.Stderr, i18n.G("Unknown channel type for client %q: %s")+"\n", nConn.RemoteAddr(), localChannel.ChannelType())
					continue
				}

				// Accept incoming channel request.
				channel, requests, err := localChannel.Accept()
				if err != nil {
					fmt.Fprintf(os.Stderr, i18n.G("Failed accepting channel client %q: %v")+"\n", err)
					return
				}

				// Sessions have out-of-band requests such as "shell", "pty-req" and "env".
				// Here we handle only the "subsystem" request.
				go func(in <-chan *ssh.Request) {
					for req := range in {
						ok := false
						switch req.Type {
						case "subsystem":
							if string(req.Payload[4:]) == "sftp" {
								ok = true
							}
						}

						_ = req.Reply(ok, nil)
					}
				}(requests)

				// Handle each channel in its own go routine.
				go func() {
					defer func() { _ = channel.Close() }()

					// Connect to the instance's SFTP server.
					sftpConn, err := sftpConn()
					if err != nil {
						fmt.Fprintf(os.Stderr, i18n.G("Failed connecting to instance SFTP for client %q: %v")+"\n", nConn.RemoteAddr(), err)
						return
					}

					defer func() { _ = sftpConn.Close() }()

					// Copy SFTP data between client and remote instance.
					ctx, cancel := context.WithCancel(ctx)
					go func() {
						_, err := io.Copy(channel, sftpConn)
						if ctx.Err() == nil {
							if err != nil {
								fmt.Fprintf(os.Stderr, i18n.G("I/O copy from instance to SSH failed: %v")+"\n", err)
							} else {
								fmt.Printf(i18n.G("Instance disconnected for client %q")+"\n", nConn.RemoteAddr())
							}
						}
						cancel() // Prevents error output when other io.Copy finishes.
						_ = channel.Close()
					}()

					_, err = io.Copy(sftpConn, channel)
					if err != nil && ctx.Err() == nil {
						fmt.Fprintf(os.Stderr, i18n.G("I/O copy from SSH to instance failed: %v")+"\n", err)
					}

					cancel() // Prevents error output when other io.Copy finishes.
					_ = sftpConn.Close()
				}()
			}
		}()
	}
}
