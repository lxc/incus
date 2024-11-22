package log

import (
	"fmt"
	"strconv"
	"strings"

	"go.starlark.net/starlark"

	"github.com/lxc/incus/v6/shared/logger"
)

// createLogger creates a logger for scriptlets.
func CreateLogger(l logger.Logger, name string) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var sb strings.Builder
		for _, arg := range args {
			s, err := strconv.Unquote(arg.String())
			if err != nil {
				s = arg.String()
			}

			sb.WriteString(s)
		}

		switch b.Name() {
		case "log_info":
			l.Info(fmt.Sprintf("%s: %s", name, sb.String()))
		case "log_warn":
			l.Warn(fmt.Sprintf("%s: %s", name, sb.String()))
		default:
			l.Error(fmt.Sprintf("%s: %s", name, sb.String()))
		}

		return starlark.None, nil
	}
}
