package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/termios"
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
	set := make(map[string]interface{})
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

	for i := 0; i < typ.NumField(); i++ {
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
func getServerSupportedFilters(filters []string, i interface{}) ([]string, []string) {
	supportedFilters := []string{}
	unsupportedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		// Only key/value pairs are supported by server side API
		// Only keys which are part of struct are supported by server side API
		// Multiple values (separated by ',') are not supported by server side API
		// Keys with '.' in name are not supported
		if len(membs) < 2 || !structHasField(reflect.TypeOf(i), membs[0]) || strings.Contains(membs[1], ",") || strings.Contains(membs[0], ".") {
			unsupportedFilters = append(unsupportedFilters, filter)
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
				return []byte{}, fmt.Errorf(i18n.G("No text editor found, please set the EDITOR environment variable"))
			}
		}
	}

	if inPath == "" {
		// If provided input, create a new file
		f, err = os.CreateTemp("", "incus_editor_")
		if err != nil {
			return []byte{}, err
		}

		revert := revert.New()
		defer revert.Fail()
		revert.Add(func() {
			_ = f.Close()
			_ = os.Remove(f.Name())
		})

		err = os.Chmod(f.Name(), 0600)
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

		revert.Success()
		revert.Add(func() { _ = os.Remove(path) })
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
				list = append(list[:j], list[j+1:]...)
				break
			}
		}

		if match {
			elements = append(elements[:i], elements[i+1:]...)
		}
	}

	return list
}
