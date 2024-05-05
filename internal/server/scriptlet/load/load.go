package load

import (
	"fmt"
	"slices"
	"sync"

	"go.starlark.net/starlark"
)

// nameInstancePlacement is the name used in Starlark for the instance placement scriptlet.
const nameInstancePlacement = "instance_placement"

// InstancePlacementCompile compiles the instance placement scriptlet.
func InstancePlacementCompile(src string) (*starlark.Program, error) {
	isPreDeclared := func(name string) bool {
		return slices.Contains([]string{
			"log_info",
			"log_warn",
			"log_error",
			"set_target",
			"get_cluster_member_resources",
			"get_cluster_member_state",
			"get_instance_resources",
			"get_instances",
			"get_cluster_members",
			"get_project",
		},
			name)
	}

	// Parse, resolve, and compile a Starlark source file.
	_, mod, err := starlark.SourceProgram(nameInstancePlacement, src, isPreDeclared)
	if err != nil {
		return nil, err
	}

	return mod, nil
}

// InstancePlacementValidate validates the instance placement scriptlet.
func InstancePlacementValidate(src string) error {
	_, err := InstancePlacementCompile(src)
	return err
}

var programsMu sync.Mutex
var programs = make(map[string]*starlark.Program)

// InstancePlacementSet compiles the instance placement scriptlet into memory for use with InstancePlacementRun.
// If empty src is provided the current program is deleted.
func InstancePlacementSet(src string) error {
	if src == "" {
		programsMu.Lock()
		delete(programs, nameInstancePlacement)
		programsMu.Unlock()
	} else {
		prog, err := InstancePlacementCompile(src)
		if err != nil {
			return err
		}

		programsMu.Lock()
		programs[nameInstancePlacement] = prog
		programsMu.Unlock()
	}

	return nil
}

// InstancePlacementProgram returns the precompiled instance placement scriptlet program.
func InstancePlacementProgram() (*starlark.Program, *starlark.Thread, error) {
	programsMu.Lock()
	prog, found := programs[nameInstancePlacement]
	programsMu.Unlock()
	if !found {
		return nil, nil, fmt.Errorf("Instance placement scriptlet not loaded")
	}

	thread := &starlark.Thread{Name: nameInstancePlacement}

	return prog, thread, nil
}
