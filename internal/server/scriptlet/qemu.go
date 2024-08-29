package scriptlet

import (
	"encoding/json"
	"fmt"

	"go.starlark.net/starlark"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/qmp"
	scriptletLoad "github.com/lxc/incus/v6/internal/server/scriptlet/load"
	"github.com/lxc/incus/v6/shared/logger"
)

// QEMURun runs the QEMU scriptlet.
func QEMURun(l logger.Logger, m *qmp.Monitor, instance string, stage string) error {
	logFunc := createLogger(l, "QEMU scriptlet ("+stage+")")
	runQMPFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var command *starlark.Dict

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "command", &command)
		if err != nil {
			return nil, err
		}

		value, err := StarlarkUnmarshal(command)
		if err != nil {
			return nil, err
		}

		request, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}

		var resp map[string]any
		err = m.RunJSON(request, &resp)
		if err != nil {
			return nil, err
		}

		rv, err := StarlarkMarshal(resp)
		if err != nil {
			return nil, fmt.Errorf("Marshalling QMP response failed: %w", err)
		}

		return rv, nil
	}

	// Remember to match the entries in scriptletLoad.QEMUCompile() with this list so Starlark can
	// perform compile time validation of functions used.
	env := starlark.StringDict{
		"log_info":  starlark.NewBuiltin("log_info", logFunc),
		"log_warn":  starlark.NewBuiltin("log_warn", logFunc),
		"log_error": starlark.NewBuiltin("log_error", logFunc),
		"run_qmp":   starlark.NewBuiltin("run_qmp", runQMPFunc),
	}

	prog, thread, err := scriptletLoad.QEMUProgram(instance)
	if err != nil {
		return err
	}

	globals, err := prog.Init(thread, env)
	if err != nil {
		return fmt.Errorf("Failed initializing: %w", err)
	}

	globals.Freeze()

	// Retrieve a global variable from starlark environment.
	qemuHook := globals["qemu_hook"]
	if qemuHook == nil {
		return fmt.Errorf("Scriptlet missing qemu_hook function")
	}

	// Call starlark function from Go.
	v, err := starlark.Call(thread, qemuHook, nil, []starlark.Tuple{
		{
			starlark.String("stage"),
			starlark.String(stage),
		},
	})
	if err != nil {
		return fmt.Errorf("Failed to run: %w", err)
	}

	if v.Type() != "NoneType" {
		return fmt.Errorf("Failed with unexpected return value: %v", v)
	}

	return nil
}
