package main

import (
	"context"
	"maps"
	"net/http"

	clusterConfig "github.com/lxc/incus/v7/internal/server/cluster/config"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/node"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/proxy"
)

func daemonConfigRender(s *state.State) (map[string]string, error) {
	config := map[string]string{}

	// Turn the config into a JSON-compatible map.
	maps.Copy(config, s.GlobalConfig.Dump())

	// Apply the local config.
	err := s.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		nodeConfig, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		maps.Copy(config, nodeConfig.Dump())

		return nil
	})
	if err != nil {
		return nil, err
	}

	return config, nil
}

// daemonConfigETag returns the config used for ETag checks, excluding node-local keys on untargeted cluster requests.
func daemonConfigETag(s *state.State, r *http.Request) (map[string]string, error) {
	if s.ServerClustered && request.QueryParam(r, "target") == "" {
		return s.GlobalConfig.Dump(), nil
	}

	return daemonConfigRender(s)
}

func daemonConfigSetProxy(d *Daemon, config *clusterConfig.Config) {
	// Update the cached proxy function
	d.proxy = proxy.FromConfig(
		config.ProxyHTTPS(),
		config.ProxyHTTP(),
		config.ProxyIgnoreHosts(),
	)
}
