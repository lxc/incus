package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/i18n"
	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/util"
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

	for k, v := range aliases {
		foundAlias = true
		for i, key := range strings.Split(k, " ") {
			if len(origArgs) <= i+1 || origArgs[i+1] != key {
				foundAlias = false
				break
			}
		}

		if foundAlias {
			aliasKey = strings.Split(k, " ")
			aliasValue = strings.Split(v, " ")
			break
		}
	}

	return aliasKey, aliasValue, foundAlias
}

func expandAlias(conf *config.Config, args []string) ([]string, bool, error) {
	var completion = false
	var completionFrament string
	var newArgs []string
	var origArgs []string

	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "-") {
			break
		}

		newArgs = append(newArgs, arg)
	}

	origArgs = append([]string{args[0]}, args[len(newArgs)+1:]...)

	// strip out completion subcommand and fragment from end
	if len(origArgs) >= 3 && origArgs[1] == "__complete" {
		completion = true
		completionFrament = origArgs[len(origArgs)-1]
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
		newArgs = append(newArgs, completionFrament)
	}

	// Add the rest of the arguments only if @ARGS@ wasn't used.
	if !hasReplacedArgsVar {
		newArgs = append(newArgs, atArgs...)
	}

	return newArgs, true, nil
}

func execIfAliases() error {
	args := os.Args

	// Avoid loops
	if os.Getenv("INCUS_ALIASES") == "1" {
		return nil
	}

	// Figure out the config directory and config path
	var configDir string
	if os.Getenv("INCUS_CONF") != "" {
		configDir = os.Getenv("INCUS_CONF")
	} else if os.Getenv("HOME") != "" {
		configDir = path.Join(os.Getenv("HOME"), ".config", "incus")
	} else {
		user, err := user.Current()
		if err != nil {
			return nil
		}

		configDir = path.Join(user.HomeDir, ".config", "incus")
	}

	confPath := os.ExpandEnv(path.Join(configDir, "config.yml"))

	// Load the configuration
	var conf *config.Config
	var err error
	if util.PathExists(confPath) {
		conf, err = config.LoadConfig(confPath)
		if err != nil {
			return nil
		}
	} else {
		conf = config.NewConfig(filepath.Dir(confPath), true)
	}

	// Expand the aliases
	newArgs, expanded, err := expandAlias(conf, args)
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
