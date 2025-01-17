package scriptlet

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/starlark"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
	"github.com/lxc/incus/v6/internal/server/instance/drivers/qmp"
	scriptletLoad "github.com/lxc/incus/v6/internal/server/scriptlet/load"
	"github.com/lxc/incus/v6/internal/server/scriptlet/log"
	"github.com/lxc/incus/v6/internal/server/scriptlet/marshal"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// qemuCfgSection is a temporary struct to hold QEMU configuration sections with Entries of type
// map[string]string instead of []cfg.Entry. This type should be moved to the cfg package in the
// future.
type qemuCfgSection struct {
	Name    string            `json:"name"`
	Comment string            `json:"comment"`
	Entries map[string]string `json:"entries"`
}

// marshalQEMUConf marshals a configuration into a []map[string]any.
func marshalQEMUConf(conf any) ([]map[string]any, error) {
	jsonConf, err := json.Marshal(conf)
	if err != nil {
		return nil, err
	}

	var newConf []map[string]any
	err = json.Unmarshal(jsonConf, &newConf)
	if err != nil {
		return nil, err
	}

	return newConf, nil
}

// unmarshalQEMUConf unmarshals a configuration into a []qemuCfgSection.
func unmarshalQEMUConf(conf any) ([]qemuCfgSection, error) {
	jsonConf, err := json.Marshal(conf)
	if err != nil {
		return nil, err
	}

	var newConf []qemuCfgSection
	err = json.Unmarshal(jsonConf, &newConf)
	if err != nil {
		return nil, err
	}

	return newConf, nil
}

// QEMURun runs the QEMU scriptlet.
func QEMURun(l logger.Logger, instance *api.Instance, cmdArgs *[]string, conf *[]cfg.Section, m *qmp.Monitor, stage string) error {
	logFunc := log.CreateLogger(l, "QEMU scriptlet ("+stage+")")

	// We first convert from []cfg.Section to []qemuCfgSection. This conversion is temporary.
	var cfgSectionMaps []qemuCfgSection
	for _, section := range *conf {
		entries := map[string]string{}
		for _, entry := range section.Entries {
			entries[entry.Key] = entry.Value
		}

		cfgSectionMaps = append(cfgSectionMaps, qemuCfgSection{Name: section.Name, Comment: section.Comment, Entries: entries})
	}

	// We do not want to handle a qemuCfgSection object within our scriptlet, for simplicity.
	cfgSections, err := marshalQEMUConf(cfgSectionMaps)
	if err != nil {
		return err
	}

	assertQEMUStarted := func(name string) error {
		if stage == "config" {
			return fmt.Errorf("%s cannot be called at config stage", name)
		}

		return nil
	}

	assertConfigStage := func(name string) error {
		if stage != "config" {
			return fmt.Errorf("%s can only be called at config stage", name)
		}

		return nil
	}

	runQMPFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		err := assertQEMUStarted(b.Name())
		if err != nil {
			return nil, err
		}

		var command *starlark.Dict
		err = starlark.UnpackArgs(b.Name(), args, kwargs, "command", &command)
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
		err := assertQEMUStarted(b.Name())
		if err != nil {
			return nil, err
		}

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
			err := assertQEMUStarted(b.Name())
			if err != nil {
				return nil, err
			}

			frame := thread.CallFrame(1)
			errPrefix := fmt.Sprintf("%s (%d:%d):", b.Name(), frame.Pos.Line, frame.Pos.Col)

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

		return starlark.NewBuiltin(strings.ReplaceAll(funName, "-", "_"), fun)
	}

	getCmdArgsFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		err := starlark.UnpackArgs(b.Name(), args, kwargs)
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(cmdArgs)
		if err != nil {
			return nil, fmt.Errorf("Marshalling QEMU command-line arguments failed: %w", err)
		}

		return rv, nil
	}

	setCmdArgsFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		err := assertConfigStage(b.Name())
		if err != nil {
			return nil, err
		}

		var newCmdArgsv *starlark.List
		err = starlark.UnpackArgs(b.Name(), args, kwargs, "args", &newCmdArgsv)
		if err != nil {
			return nil, err
		}

		newCmdArgsAny, err := marshal.StarlarkUnmarshal(newCmdArgsv)
		if err != nil {
			return nil, err
		}

		newCmdArgsListAny, ok := newCmdArgsAny.([]any)
		if !ok {
			return nil, fmt.Errorf("%s requires a list of strings", b.Name())
		}

		// Check whether -bios or -kernel are in the new arguments, and convert them to string on the go.
		var newFoundBios, newFoundKernel bool
		var newCmdArgs []string
		for _, argAny := range newCmdArgsListAny {
			arg, ok := argAny.(string)
			if !ok {
				return nil, fmt.Errorf("%s requires a list of strings", b.Name())
			}

			newCmdArgs = append(newCmdArgs, arg)

			if arg == "-bios" {
				newFoundBios = true
			} else if arg == "-kernel" {
				newFoundKernel = true
			}
		}

		// Check whether -bios or -kernel are in the current arguments
		var foundBios, foundKernel bool
		for _, arg := range *cmdArgs {
			if arg == "-bios" {
				foundBios = true
			} else if arg == "-kernel" {
				foundKernel = true
			}

			// If we've found both already, we can break early.
			if foundBios && foundKernel {
				break
			}
		}

		if foundBios != newFoundBios || foundKernel != newFoundKernel {
			return nil, errors.New("Addition or deletion of -bios or -kernel is unsupported")
		}

		*cmdArgs = newCmdArgs
		return starlark.None, nil
	}

	getConfFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		err := starlark.UnpackArgs(b.Name(), args, kwargs)
		if err != nil {
			return nil, err
		}

		rv, err := marshal.StarlarkMarshal(cfgSections)
		if err != nil {
			return nil, fmt.Errorf("Marshalling QEMU configuration failed: %w", err)
		}

		return rv, nil
	}

	setConfFunc := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		err := assertConfigStage(b.Name())
		if err != nil {
			return nil, err
		}

		var newConf *starlark.List
		err = starlark.UnpackArgs(b.Name(), args, kwargs, "conf", &newConf)
		if err != nil {
			return nil, err
		}

		confAny, err := marshal.StarlarkUnmarshal(newConf)
		if err != nil {
			return nil, err
		}

		confListAny, ok := confAny.([]any)
		if !ok {
			return nil, fmt.Errorf("%s requires a valid configuration", b.Name())
		}

		var newCfgSections []map[string]any
		for _, section := range confListAny {
			newSection, ok := section.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s requires a valid configuration", b.Name())
			}

			newCfgSections = append(newCfgSections, newSection)
		}

		// We want to further check the configuration structure, by trying to unmarshal it to a
		// []qemuCfgSection.
		_, err = unmarshalQEMUConf(confAny)
		if err != nil {
			return nil, fmt.Errorf("%s requires a valid configuration", b.Name())
		}

		cfgSections = newCfgSections
		return starlark.None, nil
	}

	// Remember to match the entries in scriptletLoad.QEMUCompile() with this list so Starlark can
	// perform compile time validation of functions used.
	env := starlark.StringDict{
		"log_info":  starlark.NewBuiltin("log_info", logFunc),
		"log_warn":  starlark.NewBuiltin("log_warn", logFunc),
		"log_error": starlark.NewBuiltin("log_error", logFunc),

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

		"get_qemu_cmdline": starlark.NewBuiltin("get_qemu_cmdline", getCmdArgsFunc),
		"set_qemu_cmdline": starlark.NewBuiltin("set_qemu_cmdline", setCmdArgsFunc),
		"get_qemu_conf":    starlark.NewBuiltin("get_qemu_conf", getConfFunc),
		"set_qemu_conf":    starlark.NewBuiltin("set_qemu_conf", setConfFunc),
	}

	prog, thread, err := scriptletLoad.QEMUProgram(instance.Name)
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

	instancev, err := marshal.StarlarkMarshal(instance)
	if err != nil {
		return fmt.Errorf("Marshalling instance failed: %w", err)
	}

	// Call starlark function from Go.
	v, err := starlark.Call(thread, qemuHook, nil, []starlark.Tuple{
		{
			starlark.String("instance"),
			instancev,
		},
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

	// We need to convert the configuration back to a suitable format
	cfgSectionMaps, err = unmarshalQEMUConf(cfgSections)
	if err != nil {
		return err
	}

	// We convert back from []qemuCfgSection to []cfg.Section. This conversion is temporary.
	var newConf []cfg.Section
	for _, section := range cfgSectionMaps {
		entries := []cfg.Entry{}
		for key, value := range section.Entries {
			entries = append(entries, cfg.Entry{Key: key, Value: value})
		}

		newConf = append(newConf, cfg.Section{Name: section.Name, Comment: section.Comment, Entries: entries})
	}

	*conf = newConf

	return nil
}
