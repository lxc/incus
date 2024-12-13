package load

import (
	"fmt"
	"slices"
	"sort"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

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
