package drivers

import (
	"context"

	"github.com/lxc/incus/v6/shared/logger"
)

// driver is the extended internal interface.
type driver interface {
	Driver

	init(logger logger.Logger, path string)
	load(ctx context.Context) error
}

// Driver represents a low-level fs notification driver.
type Driver interface {
	Name() string
	PrefixPath() string
	Watch(path string, identifier string, f func(path string, event string) bool) error
	Unwatch(path string, identifier string) error
}
