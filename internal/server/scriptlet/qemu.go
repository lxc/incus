package scriptlet

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.starlark.net/starlark"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/qmp"
	scriptletLoad "github.com/lxc/incus/v6/internal/server/scriptlet/load"
	"github.com/lxc/incus/v6/internal/server/scriptlet/log"
	"github.com/lxc/incus/v6/internal/server/scriptlet/marshal"
	"github.com/lxc/incus/v6/shared/logger"
)

// QEMURun runs the QEMU scriptlet.
func QEMURun(l logger.Logger, m *qmp.Monitor, instance string, stage string) error {
	logFunc := log.CreateLogger(l, "QEMU scriptlet ("+stage+")")
	runQMPFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var command *starlark.Dict

		err := starlark.UnpackArgs(b.Name(), args, kwargs, "command", &command)
		if err != nil {
			return nil, err
		}

		value, err := marshal.StarlarkUnmarshal(command)
		if err != nil {
			return nil, err
		}

		request, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}

		var resp map[string]any
		err = m.RunJSON(request, &resp, true)
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(resp)
		if err != nil {
			return nil, fmt.Errorf("Marshalling QMP response failed: %w", err)
		}

		return rv, nil
	}

	runCommandFromKwargs := func(funName string, kwargs []starlark.Tuple) (starlark.Value, error) {
		qmpArgs := make(map[string]any)
		for _, kwarg := range kwargs {
			key, err := marshal.StarlarkUnmarshal(kwarg.Index(0))
			if err != nil {
				return nil, err
			}

			value, err := marshal.StarlarkUnmarshal(kwarg.Index(1))
			if err != nil {
				return nil, err
			}

			qmpArgs[key.(string)] = value
		}

		var resp struct {
			Return any
		}

		err := m.Run(funName, qmpArgs, &resp)
		if err != nil {
			return nil, err
		}

		// Extract the return value
		rv, err := marshal.StarlarkMarshal(resp.Return)
		if err != nil {
			return nil, fmt.Errorf("Marshalling QMP response failed: %w", err)
		}

		return rv, nil
	}

	runCommandFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		frame := thread.CallFrame(1)
		errPrefix := fmt.Sprintf("run_command (%d:%d):", frame.Pos.Line, frame.Pos.Col)

		argsLen := args.Len()
		if argsLen != 1 {
			return nil, fmt.Errorf("%s Expected exactly one positional argument, got %d", errPrefix, argsLen)
		}

		arg, err := marshal.StarlarkUnmarshal(args.Index(0))
		if err != nil {
			return nil, err
		}

		funName, ok := arg.(string)
		if !ok {
			return nil, fmt.Errorf("%s The positional argument must be a string representing a QMP function", errPrefix)
		}

		rv, err := runCommandFromKwargs(funName, kwargs)
		if err != nil {
			return nil, err
		}

		return rv, nil
	}

	makeQOM := func(funName string) *starlark.Builtin {
		fun := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			frame := thread.CallFrame(1)
			errPrefix := fmt.Sprintf("%s (%d:%d):", strings.ReplaceAll(funName, "-", "_"), frame.Pos.Line, frame.Pos.Col)

			argsLen := args.Len()
			if argsLen != 0 {
				return nil, fmt.Errorf("%s Expected only keyword arguments, got %d positional argument(s)", errPrefix, argsLen)
			}

			rv, err := runCommandFromKwargs(funName, kwargs)
			if err != nil {
				return nil, err
			}

			return rv, nil
		}

		return starlark.NewBuiltin(funName, fun)
	}

	// Remember to match the entries in scriptletLoad.QEMUCompile() with this list so Starlark can
	// perform compile time validation of functions used.
	env := starlark.StringDict{
		"log_info":       starlark.NewBuiltin("log_info", logFunc),
		"log_warn":       starlark.NewBuiltin("log_warn", logFunc),
		"log_error":      starlark.NewBuiltin("log_error", logFunc),
		"run_qmp":        starlark.NewBuiltin("run_qmp", runQMPFunc),
		"run_command":    starlark.NewBuiltin("run_command", runCommandFunc),
		"blockdev_add":   makeQOM("blockdev-add"),
		"blockdev_del":   makeQOM("blockdev-del"),
		"chardev_add":    makeQOM("chardev-add"),
		"chardev_change": makeQOM("chardev-change"),
		"chardev_remove": makeQOM("chardev-remove"),
		"device_add":     makeQOM("device_add"),
		"device_del":     makeQOM("device_del"),
		"netdev_add":     makeQOM("netdev_add"),
		"netdev_del":     makeQOM("netdev_del"),
		"object_add":     makeQOM("object-add"),
		"object_del":     makeQOM("object-del"),
		"qom_get":        makeQOM("qom-get"),
		"qom_list":       makeQOM("qom-list"),
		"qom_set":        makeQOM("qom-set"),
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
