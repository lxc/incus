package usage

import (
	"fmt"

	"github.com/fatih/color"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/cliconfig"
)

func getInstanceServer(conf Config, servers map[string]incus.InstanceServer, remoteName string) (incus.InstanceServer, error) {
	// Look for a the remote in our cache.
	remoteServer, ok := servers[remoteName]
	if !ok {
		// New connection
		d, err := conf.CLIConfig.GetInstanceServer(remoteName)
		if err != nil {
			return nil, err
		}

		servers[remoteName] = d
		remoteServer = d
	}

	if remoteName != "local" {
		info, err := remoteServer.GetConnectionInfo()
		if err == nil && info.URL != conf.CLIConfig.Remotes[remoteName].LastWorkingAddr {
			remote := conf.CLIConfig.Remotes[remoteName]
			remote.LastWorkingAddr = info.URL
			conf.CLIConfig.Remotes[remoteName] = remote
			_ = conf.SaveCLIConfig()
		}
	}

	return remoteServer, nil
}

// ParseString returns a parsed atom corresponding to a single string.
func ParseString(s string) *Parsed {
	p, _ := placeholder{}.Parse(Config{RTL: false}, nil, &[]string{s})
	return p
}

// ParseDefault returns a parsed atom corresponding to how the given atom is parsed without any
// argument.
func ParseDefault(atom Atom, conf *cliconfig.Config) (*Parsed, error) {
	return atom.Parse(Config{CLIConfig: conf, RTL: false}, map[string]incus.InstanceServer{}, &[]string{})
}

// renderRaw returns the atom rendered after disabling terminal coloring.
func renderRaw(atom Atom) string {
	noColor := color.NoColor
	color.NoColor = true
	s := atom.Render()
	color.NoColor = noColor
	return s
}

func quote(s string) string {
	return fmt.Sprintf(i18n.G("“%s”"), s)
}
