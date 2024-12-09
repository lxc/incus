package load

import (
	"fmt"
	"slices"
	"sort"
	"sync"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// nameInstancePlacement is the name used in Starlark for the instance placement scriptlet.
const nameInstancePlacement = "instance_placement"

// prefixQEMU is the prefix used in Starlark for the QEMU scriptlet.
const prefixQEMU = "qemu"

// nameAuthorization is the name used in Starlark for the Authorization scriptlet.
const nameAuthorization = "authorization"

// compile compiles a scriptlet.
func compile(programName string, src string, preDeclared []string) (*starlark.Program, error) {
	isPreDeclared := func(name string) bool {
		return slices.Contains(preDeclared, name)
	}

	// Parse, resolve, and compile a Starlark source file.
	_, mod, err := starlark.SourceProgramOptions(syntax.LegacyFileOptions(), programName, src, isPreDeclared)
	if err != nil {
		return nil, err
	}

	return mod, nil
}

// validate validates a scriptlet by compiling it and checking the presence of required functions.
func validate(compiler func(string, string) (*starlark.Program, error), programName string, src string, requiredFunctions map[string][]string) error {
	prog, err := compiler(programName, src)
	if err != nil {
		return err
	}

	thread := &starlark.Thread{Name: programName}
	globals, err := prog.Init(thread, nil)
	if err != nil {
		return err
	}

	globals.Freeze()

	var notFound []string
	for funName, requiredArgs := range requiredFunctions {
		// The function is missing if its name is not found in the globals.
		funv := globals[funName]
		if funv == nil {
			notFound = append(notFound, funName)
			continue
		}

		// The function is missing if its name is not bound to a function.
		fun, ok := funv.(*starlark.Function)
		if !ok {
			notFound = append(notFound, funName)
		}

		// Get the function arguments.
		argc := fun.NumParams()
		var args []string
		for i := range argc {
			arg, _ := fun.Param(i)
			args = append(args, arg)
		}

		// Return an error early if the function does not have the right arguments.
		match := len(args) == len(requiredArgs)
		if match {
			sort.Strings(args)
			sort.Strings(requiredArgs)
			for i := range args {
				if args[i] != requiredArgs[i] {
					match = false
					break
				}
			}
		}

		if !match {
			return fmt.Errorf("The function %q defines arguments %q (expected: %q)", funName, args, requiredArgs)
		}
	}

	switch len(notFound) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("The function %q is required but has not been found in the scriptlet", notFound[0])
	default:
		return fmt.Errorf("The functions %q are required but have not been found in the scriptlet", notFound)
	}
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
		"get_instances_count",
		"get_cluster_members",
		"get_project",
	})
}

// InstancePlacementValidate validates the instance placement scriptlet.
func InstancePlacementValidate(src string) error {
	return validate(InstancePlacementCompile, nameInstancePlacement, src, map[string][]string{
		"instance_placement": {"request", "candidate_members"},
	})
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

// QEMUCompile compiles the QEMU scriptlet.
func QEMUCompile(name string, src string) (*starlark.Program, error) {
	return compile(name, src, []string{
		"log_info",
		"log_warn",
		"log_error",
		"run_qmp",
		"run_command",
		"blockdev_add",
		"blockdev_del",
		"chardev_add",
		"chardev_change",
		"chardev_remove",
		"device_add",
		"device_del",
		"netdev_add",
		"netdev_del",
		"object_add",
		"object_del",
		"qom_get",
		"qom_list",
		"qom_set",
	})
}

// QEMUValidate validates the QEMU scriptlet.
func QEMUValidate(src string) error {
	return validate(QEMUCompile, prefixQEMU, src, map[string][]string{
		"qemu_hook": {"hook_name"},
	})
}

// QEMUSet compiles the QEMU scriptlet into memory for use with QEMURun.
// If empty src is provided the current program is deleted.
func QEMUSet(src string, instance string) error {
	return set(QEMUCompile, prefixQEMU+"/"+instance, src)
}

// QEMUProgram returns the precompiled QEMU scriptlet program.
func QEMUProgram(instance string) (*starlark.Program, *starlark.Thread, error) {
	return program("QEMU", prefixQEMU+"/"+instance)
}

// AuthorizationCompile compiles the authorization scriptlet.
func AuthorizationCompile(name string, src string) (*starlark.Program, error) {
	return compile(name, src, []string{
		"log_info",
		"log_warn",
		"log_error",
	})
}

// AuthorizationValidate validates the authorization scriptlet.
func AuthorizationValidate(src string) error {
	return validate(AuthorizationCompile, nameAuthorization, src, map[string][]string{
		"authorize": {"details", "object", "entitlement"},
	})
}

// AuthorizationSet compiles the authorization scriptlet into memory for use with AuthorizationRun.
// If empty src is provided the current program is deleted.
func AuthorizationSet(src string) error {
	return set(AuthorizationCompile, nameAuthorization, src)
}

// AuthorizationProgram returns the precompiled authorization scriptlet program.
func AuthorizationProgram() (*starlark.Program, *starlark.Thread, error) {
	return program("Authorization", nameAuthorization)
}
