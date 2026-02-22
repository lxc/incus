package usage

import (
	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/cliconfig"
)

func getInstanceServer(conf *cliconfig.Config, servers map[string]incus.InstanceServer, remoteName string) (incus.InstanceServer, error) {
	// Look for a the remote in our cache.
	remoteServer, ok := servers[remoteName]
	if !ok {
		// New connection
		d, err := conf.GetInstanceServer(remoteName)
		if err != nil {
			return nil, err
		}

		servers[remoteName] = d
		remoteServer = d
	}

	return remoteServer, nil
}
