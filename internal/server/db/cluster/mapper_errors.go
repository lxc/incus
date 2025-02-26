package cluster

import (
	"errors"
	"net/http"

	"github.com/lxc/incus/v6/shared/api"
)

func init() {
	mapErr = clusterMapErr
}

func clusterMapErr(err error, entity string) error {
	if errors.Is(err, ErrNotFound) {
		return api.StatusErrorf(http.StatusNotFound, "%s not found", entity)
	}

	if errors.Is(err, ErrConflict) {
		return api.StatusErrorf(http.StatusConflict, "This %q entry already exists", entity)
	}

	return err
}
