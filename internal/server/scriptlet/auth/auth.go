package auth

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/lxc/incus/v6/internal/server/auth/common"
	scriptletLoad "github.com/lxc/incus/v6/internal/server/scriptlet/load"
	"github.com/lxc/incus/v6/internal/server/scriptlet/log"
	"github.com/lxc/incus/v6/internal/server/scriptlet/marshal"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// AuthorizationRun runs the authorization scriptlet.
func AuthorizationRun(l logger.Logger, details *common.RequestDetails, object string, entitlement string) (bool, error) {
	logFunc := log.CreateLogger(l, "Authorization scriptlet")

	// Remember to match the entries in scriptletLoad.AuthorizationCompile() with this list so Starlark can
	// perform compile time validation of functions used.
	env := starlark.StringDict{
		"log_info":  starlark.NewBuiltin("log_info", logFunc),
		"log_warn":  starlark.NewBuiltin("log_warn", logFunc),
		"log_error": starlark.NewBuiltin("log_error", logFunc),
	}

	prog, thread, err := scriptletLoad.AuthorizationProgram()
	if err != nil {
		return false, err
	}

	globals, err := prog.Init(thread, env)
	if err != nil {
		return false, fmt.Errorf("Failed initializing: %w", err)
	}

	globals.Freeze()

	// Retrieve a global variable from starlark environment.
	authorizer := globals["authorize"]
	if authorizer == nil {
		return false, fmt.Errorf("Scriptlet missing authorize function")
	}

	detailsv, err := marshal.StarlarkMarshal(details)
	if err != nil {
		return false, fmt.Errorf("Marshalling details failed: %w", err)
	}

	// Call starlark function from Go.
	v, err := starlark.Call(thread, authorizer, nil, []starlark.Tuple{
		{
			starlark.String("details"),
			detailsv,
		}, {
			starlark.String("object"),
			starlark.String(object),
		}, {
			starlark.String("entitlement"),
			starlark.String(entitlement),
		},
	})
	if err != nil {
		return false, fmt.Errorf("Failed to run: %w", err)
	}

	if v.Type() != "bool" {
		return false, fmt.Errorf("Failed with unexpected return value: %v", v)
	}

	return bool(v.(starlark.Bool)), nil
}

func getAccess(l logger.Logger, fun string, args []starlark.Tuple) (*api.Access, error) {
	access := &api.Access{}
	emptyAccess := &api.Access{}
	logFunc := log.CreateLogger(l, fmt.Sprintf("Authorization scriptlet (%s)", fun))

	// Remember to match the entries in scriptletLoad.AuthorizationCompile() with this list so Starlark can
	// perform compile time validation of functions used.
	env := starlark.StringDict{
		"log_info":  starlark.NewBuiltin("log_info", logFunc),
		"log_warn":  starlark.NewBuiltin("log_warn", logFunc),
		"log_error": starlark.NewBuiltin("log_error", logFunc),
	}

	prog, thread, err := scriptletLoad.AuthorizationProgram()
	if err != nil {
		return emptyAccess, err
	}

	globals, err := prog.Init(thread, env)
	if err != nil {
		return emptyAccess, fmt.Errorf("Failed initializing: %w", err)
	}

	globals.Freeze()

	// Retrieve a global variable from starlark environment.
	getter := globals[fun]
	if getter == nil {
		return emptyAccess, nil
	}

	// Call starlark function from Go.
	v, err := starlark.Call(thread, getter, nil, args)
	if err != nil {
		return emptyAccess, fmt.Errorf("Failed to run: %w", err)
	}

	value, err := marshal.StarlarkUnmarshal(v)
	if err != nil {
		return emptyAccess, err
	}

	identifiers, ok := value.([]any)
	if !ok {
		return emptyAccess, fmt.Errorf("Failed with unexpected return value: %v", v)
	}

	for _, id := range identifiers {
		identifier, ok := id.(string)
		if !ok {
			return emptyAccess, fmt.Errorf("Failed with unexpected return value: %v", v)
		}

		*access = append(*access, api.AccessEntry{
			Identifier: identifier,
			Role:       "unknown",
			Provider:   "scriptlet",
		})
	}

	return access, nil
}

// GetInstanceAccessRun runs the optional get_instance_access scriptlet function.
func GetInstanceAccessRun(l logger.Logger, projectName string, instanceName string) (*api.Access, error) {
	return getAccess(l, "get_instance_access", []starlark.Tuple{
		{
			starlark.String("project_name"),
			starlark.String(projectName),
		}, {
			starlark.String("instance_name"),
			starlark.String(instanceName),
		},
	})
}

// GetProjectAccessRun runs the optional get_project_access scriptlet function.
func GetProjectAccessRun(l logger.Logger, projectName string) (*api.Access, error) {
	return getAccess(l, "get_project_access", []starlark.Tuple{
		{
			starlark.String("project_name"),
			starlark.String(projectName),
		},
	})
}
