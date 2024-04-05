//go:build !linux || !cgo || agent

package state

import (
	"context"

	"github.com/lxc/incus/v6/internal/server/events"
)

// State here is just an empty shim to statisfy dependencies.
type State struct {
	Events      *events.Server
	ShutdownCtx context.Context
	ServerName  string
}
