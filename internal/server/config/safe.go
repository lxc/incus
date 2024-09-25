package config

import (
	"fmt"

	"github.com/lxc/incus/v6/shared/logger"
)

// SafeLoad is a wrapper around Load() that does not error when invalid keys
// are found, and just logs warnings instead. Other kinds of errors are still
// returned.
func SafeLoad(schema Schema, values map[string]string) (Map, error) {
	m, err := Load(schema, values)
	if err != nil {
		errors, ok := err.(ErrorList)
		if !ok {
			return m, err
		}

		for _, error := range errors {
			message := fmt.Sprintf("Invalid configuration key: %s", error.Reason)
			logger.Error(message, logger.Ctx{"key": error.Name})
		}
	}

	return m, nil
}
