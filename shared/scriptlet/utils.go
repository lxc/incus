package scriptlet

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Loader holds the programs for the scriptlet.
type Loader struct {
	programsMu sync.Mutex
	programs   map[string]*starlark.Program
}

// argMismatch represents mismatching arguments in a function.
type argMismatch struct {
	gotten   []string
	expected []string
}

// scriptletFunction represents a possibly optional function in a scriptlet.
type scriptletFunction struct {
	name     string
	optional bool
}

// Declaration is a type alias to make scriptlet declaration easier.
type Declaration = map[scriptletFunction][]string

// NewLoader creates a new Loader.
func NewLoader() *Loader {
	return &Loader{
		programsMu: sync.Mutex{},
		programs:   map[string]*starlark.Program{},
	}
}

// Compile compiles a scriptlet.
func Compile(programName string, src string, preDeclared []string) (*starlark.Program, error) {
	isPreDeclared := func(name string) bool {
		return slices.Contains(preDeclared, name)
	}

	// Prepare options.
	opts := syntax.LegacyFileOptions()
	opts.Set = true

	// Parse, resolve, and compile a Starlark source file.
	_, mod, err := starlark.SourceProgramOptions(opts, programName, src, isPreDeclared)
	if err != nil {
		return nil, err
	}

	return mod, nil
}

// Required is a convenience wrapper declaring a required function.
func Required(name string) scriptletFunction {
	return scriptletFunction{name: name, optional: false}
}

// Optional is a convenience wrapper declaring an optional function.
func Optional(name string) scriptletFunction {
	return scriptletFunction{name: name, optional: true}
}

// optionalToString converts a Boolean describing optional functions to its string representation.
func optionalToString(optional bool) string {
	if optional {
		return "optional"
	}

	return "required"
}

// validateFunction validates a single Starlark function.
func validateFunction(funv starlark.Value, requiredArgs []string) (bool, bool, *argMismatch) {
	// The function is missing if its name is not found in the globals.
	if funv == nil {
		return true, false, nil
	}

	// The function is actually not a function if its name is not bound to a function.
	fun, ok := funv.(*starlark.Function)
	if !ok {
		return false, true, nil
	}

	// Get the function arguments.
	argc := fun.NumParams()
	var args []string
	for i := range argc {
		arg, _ := fun.Param(i)
		args = append(args, arg)
	}

	// The function is invalid if it does not have the right arguments.
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
		return false, false, &argMismatch{gotten: args, expected: requiredArgs}
	}

	return false, false, nil
}

// Validate validates a scriptlet by compiling it and checking the presence of required and optional functions.
func Validate(compiler func(string, string) (*starlark.Program, error), programName string, src string, scriptletFunctions Declaration) error {
	// Try to compile the program.
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

	var missingFuns []string
	mistypedFuns := make(map[scriptletFunction]string)
	mismatchingFuns := make(map[scriptletFunction]*argMismatch)
	errorsFound := false
	for fun, requiredArgs := range scriptletFunctions {
		funv := globals[fun.name]
		missing, mistyped, mismatch := validateFunction(funv, requiredArgs)

		if missing && !fun.optional || mistyped || mismatch != nil {
			errorsFound = true
			if missing {
				missingFuns = append(missingFuns, fun.name)
			} else if mistyped {
				mistypedFuns[fun] = funv.Type()
			} else {
				mismatchingFuns[fun] = mismatch
			}
		}
	}

	// Return early if everything looks good.
	if !errorsFound {
		return nil
	}

	errorText := ""
	sentences := 0

	// String builder to format pretty error messages.
	appendToError := func(text string) {
		var link string

		switch sentences {
		case 0:
			link = ""
		case 1:
			link = "; additionally, "
		default:
			link = "; finally, "
		}

		errorText += link
		errorText += text
		sentences++
	}

	switch len(missingFuns) {
	case 0:
	case 1:
		appendToError(fmt.Sprintf("the function %q is required but has not been found in the scriptlet", missingFuns[0]))
	default:
		appendToError(fmt.Sprintf("the functions %q are required but have not been found in the scriptlet", missingFuns))
	}

	if len(mistypedFuns) != 0 {
		var parts []string
		for fun, ty := range mistypedFuns {
			parts = append(parts, fmt.Sprintf("%q should define the scriptlet’s %s function of the same name (found a value of type %s instead)", fun.name, optionalToString(fun.optional), ty))
		}

		appendToError(strings.Join(parts, ", "))
	}

	if len(mismatchingFuns) != 0 {
		var parts []string
		for fun, args := range mismatchingFuns {
			parts = append(parts, fmt.Sprintf("the %s function %q defines arguments %q (expected %q)", optionalToString(fun.optional), fun.name, args.gotten, args.expected))
		}

		appendToError(strings.Join(parts, ", "))
	}

	return errors.New(errorText)
}

// Set compiles a scriptlet into memory. If empty src is provided the current program is deleted.
func (l *Loader) Set(compiler func(string, string) (*starlark.Program, error), programName string, src string) error {
	if src == "" {
		l.programsMu.Lock()
		delete(l.programs, programName)
		l.programsMu.Unlock()
	} else {
		prog, err := compiler(programName, src)
		if err != nil {
			return err
		}

		l.programsMu.Lock()
		l.programs[programName] = prog
		l.programsMu.Unlock()
	}

	return nil
}

// Program returns a precompiled scriptlet program.
func (l *Loader) Program(name string, programName string) (*starlark.Program, *starlark.Thread, error) {
	l.programsMu.Lock()
	prog, found := l.programs[programName]
	l.programsMu.Unlock()
	if !found {
		return nil, nil, fmt.Errorf("%s scriptlet not loaded", name)
	}

	thread := &starlark.Thread{Name: programName}

	return prog, thread, nil
}
