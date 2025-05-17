package main

import (
	"context"
	"maps"

	clusterConfig "github.com/lxc/incus/v6/internal/server/cluster/config"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/proxy"
)

func daemonConfigRender(state *state.State) (map[string]string, error) {
	config := map[string]string{}

	// Turn the config into a JSON-compatible map.
	maps.Copy(config, state.GlobalConfig.Dump())

	// Apply the local config.
	err := state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
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

func daemonConfigSetProxy(d *Daemon, config *clusterConfig.Config) {
	// Update the cached proxy function
	d.proxy = proxy.FromConfig(
		config.ProxyHTTPS(),
		config.ProxyHTTP(),
		config.ProxyIgnoreHosts(),
	)
}
