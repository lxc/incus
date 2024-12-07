package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/i18n"
	config "github.com/lxc/incus/v6/shared/cliconfig"
)

var numberedArgRegex = regexp.MustCompile(`@ARG(\d+)@`)

// defaultAliases contains LXC's built-in command line aliases.  The built-in
// aliases are checked only if no user-defined alias was found.
var defaultAliases = map[string]string{
	"shell": "exec @ARGS@ -- su -l",
}

func findAlias(aliases map[string]string, origArgs []string) ([]string, []string, bool) {
	foundAlias := false
	aliasKey := []string{}
	aliasValue := []string{}

	// Sort the aliases in a stable order, preferring the long multi-fields ones.
	aliasNames := make([]string, 0, len(aliases))
	for k := range aliases {
		aliasNames = append(aliasNames, k)
	}

	slices.Sort(aliasNames)
	slices.SortStableFunc(aliasNames, func(a, b string) int {
		aFields := strings.Split(a, " ")
		bFields := strings.Split(b, " ")

		if len(aFields) == len(bFields) {
			return 0
		} else if len(aFields) < len(bFields) {
			return 1
		}

		return -1
	})

	for _, k := range aliasNames {
		v := aliases[k]

		foundAlias = true
		for i, key := range strings.Split(k, " ") {
			if len(origArgs) <= i+1 || origArgs[i+1] != key {
				foundAlias = false
				break
			}
		}

		if foundAlias {
			aliasKey = strings.Split(k, " ")

			fields, err := shellquote.Split(v)
			if err == nil {
				aliasValue = fields
			} else {
				aliasValue = strings.Split(v, " ")
			}

			break
		}
	}

	return aliasKey, aliasValue, foundAlias
}

func expandAlias(conf *config.Config, args []string, app *cobra.Command) ([]string, bool, error) {
	fset := app.Flags()

	nargs := fset.NArg()
	firstArgIndex := 1
	firstPosArgIndex := 0
	if fset.Arg(0) == "__complete" {
		nargs--
		firstArgIndex++
		firstPosArgIndex++
	}

	if nargs == 0 {
		return nil, false, nil
	}

	lastFlagIndex := slices.Index(args, fset.Arg(firstPosArgIndex))

	// newArgs contains all the flags before the first positional argument
	newArgs := args[firstArgIndex:lastFlagIndex]

	// origArgs contains everything except the flags in newArgs
	origArgs := slices.Concat(args[:firstArgIndex], args[lastFlagIndex:])

	// strip out completion subcommand and fragment from end
	completion := false
	completionFragment := ""
	if len(origArgs) >= 3 && origArgs[1] == "__complete" {
		completion = true
		completionFragment = origArgs[len(origArgs)-1]
		origArgs = append(origArgs[:1], origArgs[2:len(origArgs)-1]...)
	}

	aliasKey, aliasValue, foundAlias := findAlias(conf.Aliases, origArgs)
	if !foundAlias {
		aliasKey, aliasValue, foundAlias = findAlias(defaultAliases, origArgs)
		if !foundAlias {
			return []string{}, false, nil
		}
	}

	if !strings.HasPrefix(aliasValue[0], "/") {
		newArgs = append([]string{origArgs[0]}, newArgs...)
	}

	// The @ARGS@ are initially any arguments given after the alias key.
	var atArgs []string
	if len(origArgs) > len(aliasKey)+1 {
		atArgs = origArgs[len(aliasKey)+1:]
	}

	// Find the arguments that have been referenced directly e.g. @ARG1@.
	numberedArgsMap := map[int]string{}
	for _, aliasArg := range aliasValue {
		matches := numberedArgRegex.FindAllStringSubmatch(aliasArg, -1)
		if len(matches) == 0 {
			continue
		}

		for _, match := range matches {
			argNoStr := match[1]
			argNo, err := strconv.Atoi(argNoStr)
			if err != nil {
				return nil, false, fmt.Errorf(i18n.G("Invalid argument %q"), match[0])
			}

			if argNo > len(atArgs) {
				return nil, false, fmt.Errorf(i18n.G("Found alias %q references an argument outside the given number"), strings.Join(aliasKey, " "))
			}

			numberedArgsMap[argNo] = atArgs[argNo-1]
		}
	}

	// Remove directly referenced arguments from @ARGS@
	for i := len(atArgs) - 1; i >= 0; i-- {
		_, ok := numberedArgsMap[i+1]
		if ok {
			atArgs = append(atArgs[:i], atArgs[i+1:]...)
		}
	}

	// Replace arguments
	hasReplacedArgsVar := false
	for _, aliasArg := range aliasValue {
		// Only replace all @ARGS@ when it is not part of another string
		if aliasArg == "@ARGS@" {
			// if completing we want to stop on @ARGS@ and append the completion below
			if completion {
				break
			} else {
				newArgs = append(newArgs, atArgs...)
			}

			hasReplacedArgsVar = true
			continue
		}

		// Replace @ARG1@, @ARG2@ etc. as substrings
		matches := numberedArgRegex.FindAllStringSubmatch(aliasArg, -1)
		if len(matches) > 0 {
			newArg := aliasArg
			for _, match := range matches {
				argNoStr := match[1]
				argNo, err := strconv.Atoi(argNoStr)
				if err != nil {
					return nil, false, fmt.Errorf(i18n.G("Invalid argument %q"), match[0])
				}

				replacement := numberedArgsMap[argNo]
				newArg = strings.Replace(newArg, match[0], replacement, -1)
			}

			newArgs = append(newArgs, newArg)
			continue
		}

		newArgs = append(newArgs, aliasArg)
	}

	// add back in completion if it was stripped before
	if completion {
		newArgs = append([]string{newArgs[0], "__complete"}, newArgs[1:]...)
		newArgs = append(newArgs, completionFragment)
	}

	// Add the rest of the arguments only if @ARGS@ wasn't used.
	if !hasReplacedArgsVar {
		newArgs = append(newArgs, atArgs...)
	}

	return newArgs, true, nil
}

func execIfAliases(app *cobra.Command) error {
	// Avoid loops
	if os.Getenv("INCUS_ALIASES") == "1" {
		return nil
	}

	conf, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to load configuration: %s"), err)
	}

	// Expand the aliases
	newArgs, expanded, err := expandAlias(conf, os.Args, app)
	if err != nil {
		return err
	} else if !expanded {
		return nil
	}

	// Look for the executable
	path, err := exec.LookPath(newArgs[0])
	if err != nil {
		return fmt.Errorf(i18n.G("Processing aliases failed: %s"), err)
	}

	// Re-exec
	environ := getEnviron()
	environ = append(environ, "INCUS_ALIASES=1")
	ret := doExec(path, newArgs, environ)
	return fmt.Errorf(i18n.G("Processing aliases failed: %s"), ret)
}
