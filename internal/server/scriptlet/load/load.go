package load

import (
	"fmt"
	"slices"
	"sync"

	"go.starlark.net/starlark"
)

// nameInstancePlacement is the name used in Starlark for the instance placement scriptlet.
const nameInstancePlacement = "instance_placement"

// compile compiles a scriptlet.
func compile(programName string, src string, preDeclared []string) (*starlark.Program, error) {
	isPreDeclared := func(name string) bool {
		return slices.Contains(preDeclared, name)
	}

	// Parse, resolve, and compile a Starlark source file.
	_, mod, err := starlark.SourceProgram(programName, src, isPreDeclared)
	if err != nil {
		return nil, err
	}

	return mod, nil
}

var programsMu sync.Mutex
var programs = make(map[string]*starlark.Program)

// set compiles a scriptlet into memory. If empty src is provided the current program is deleted.
func set(compiler func(string, string) (*starlark.Program, error), programName string, src string) error {
	if src == "" {
		programsMu.Lock()
		delete(programs, programName)
		programsMu.Unlock()
	} else {
		prog, err := compiler(programName, src)
		if err != nil {
			return err
		}

		programsMu.Lock()
		programs[programName] = prog
		programsMu.Unlock()
	}

	return nil
}

// program returns a precompiled scriptlet program.
func program(name string, programName string) (*starlark.Program, *starlark.Thread, error) {
	programsMu.Lock()
	prog, found := programs[programName]
	programsMu.Unlock()
	if !found {
		return nil, nil, fmt.Errorf("%s scriptlet not loaded", name)
	}

	thread := &starlark.Thread{Name: programName}

	return prog, thread, nil
}

// InstancePlacementCompile compiles the instance placement scriptlet.
func InstancePlacementCompile(name string, src string) (*starlark.Program, error) {
	return compile(name, src, []string{
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
	})
}

// InstancePlacementValidate validates the instance placement scriptlet.
func InstancePlacementValidate(src string) error {
	_, err := InstancePlacementCompile(nameInstancePlacement, src)
	return err
}

// InstancePlacementSet compiles the instance placement scriptlet into memory for use with InstancePlacementRun.
// If empty src is provided the current program is deleted.
func InstancePlacementSet(src string) error {
	return set(InstancePlacementCompile, nameInstancePlacement, src)
}

// InstancePlacementProgram returns the precompiled instance placement scriptlet program.
func InstancePlacementProgram() (*starlark.Program, *starlark.Thread, error) {
	return program("Instance placement", nameInstancePlacement)
}
