package config

import (
	"errors"
	"fmt"

	"github.com/lxc/incus/v6/shared/logger"
)

// SafeLoad is a wrapper around Load() that does not error when invalid keys
// are found, and just logs warnings instead. Other kinds of errors are still
// returned.
func SafeLoad(schema Schema, values map[string]string) (Map, error) {
	m, err := Load(schema, values)
	if err != nil {
		var errs *ErrorList
		ok := errors.As(err, &errs)
		if !ok {
			return m, err
		}

		for _, e := range errs.errors {
			message := fmt.Sprintf("Invalid configuration key: %s", e.reason)
			logger.Error(message, logger.Ctx{"key": e.configKey})
		}
	}

	return m, nil
}
