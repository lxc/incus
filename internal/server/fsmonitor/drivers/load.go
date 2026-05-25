package drivers

import (
	"context"

	"github.com/lxc/incus/v7/shared/logger"
)

var drivers = map[string]func() driver{
	"inotify":  func() driver { return &inotify{} },
	"fanotify": func() driver { return &fanotify{} },
}

// Load returns a Driver for an existing low-level FS monitor.
func Load(ctx context.Context, l logger.Logger, driverName string, path string) (Driver, error) {
	df, ok := drivers[driverName]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := df()

	d.init(l, path)

	err := d.load(ctx)
	if err != nil {
		return nil, err
	}

	return d, nil
}
