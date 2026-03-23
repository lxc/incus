package usage

import (
	"github.com/fatih/color"

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

// ParseString returns a parsed atom corresponding to a single string.
func ParseString(s string) *Parsed {
	p, _ := placeholder{}.Parse(nil, nil, nil, &[]string{s}, false)
	return p
}

// ParseDefault returns a parsed atom corresponding to how the given atom is parsed without any
// argument.
func ParseDefault(atom Atom, conf *cliconfig.Config) (*Parsed, error) {
	return atom.Parse(conf, nil, map[string]incus.InstanceServer{}, &[]string{}, false)
}

// renderRaw returns the atom rendered after disabling terminal coloring.
func renderRaw(atom Atom) string {
	noColor := color.NoColor
	color.NoColor = true
	s := atom.Render()
	color.NoColor = noColor
	return s
}
