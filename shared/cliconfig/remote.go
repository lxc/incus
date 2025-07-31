package cliconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// Remote holds details for communication with a remote daemon.
type Remote struct {
	Addr       string `yaml:"addr"`
	AuthType   string `yaml:"auth_type,omitempty"`
	KeepAlive  int    `yaml:"keepalive,omitempty"`
	Project    string `yaml:"project,omitempty"`
	Protocol   string `yaml:"protocol,omitempty"`
	CredHelper string `yaml:"credentials_helper,omitempty"`
	Public     bool   `yaml:"public"`
	Global     bool   `yaml:"-"`
	Static     bool   `yaml:"-"`
}

// ParseRemote splits remote and object.
func (c *Config) ParseRemote(raw string) (string, string, error) {
	result := strings.SplitN(raw, ":", 2)
	if len(result) == 1 {
		return c.DefaultRemote, raw, nil
	}

	_, ok := c.Remotes[result[0]]
	if !ok {
		// Attempt to play nice with snapshots containing ":"
		if strings.Contains(raw, "/") && strings.Contains(result[0], "/") {
			return c.DefaultRemote, raw, nil
		}

		return "", "", fmt.Errorf("The remote \"%s\" doesn't exist", result[0])
	}

	return result[0], result[1], nil
}

// GetInstanceServer returns a InstanceServer struct for the remote.
func (c *Config) GetInstanceServer(name string) (incus.InstanceServer, error) {
	// Handle "local" on non-Linux
	if name == "local" && runtime.GOOS != "linux" {
		return nil, ErrNotLinux
	}

	// Get the remote
	remote, ok := c.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("The remote \"%s\" doesn't exist", name)
	}

	// Quick checks.
	if remote.Public || remote.Protocol != "incus" {
		return nil, errors.New("The remote isn't a private server")
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Unix socket
	remoteAddr, hasUnixPrefix := strings.CutPrefix(remote.Addr, "unix:")
	if hasUnixPrefix {
		d, err := incus.ConnectIncusUnix(strings.TrimPrefix(remoteAddr, "//"), args)
		if err != nil {
			var netErr *net.OpError

			if errors.As(err, &netErr) {
				errMsg := netErr.Unwrap().Error()

				switch errMsg {
				case "connect: connection refused", "connect: no such file or directory":
					return nil, fmt.Errorf("The incus daemon doesn't appear to be started (socket path: %s)", netErr.Addr)
				case "connect: permission denied":
					return nil, fmt.Errorf("You don't have the needed permissions to talk to the incus daemon (socket path: %s)", netErr.Addr)
				}

				return nil, err
			}

			return nil, err
		}

		if remote.Project != "" && remote.Project != "default" {
			d = d.UseProject(remote.Project)
		}

		if c.ProjectOverride != "" {
			d = d.UseProject(c.ProjectOverride)
		}

		return d, nil
	}

	// HTTPs
	if !slices.Contains([]string{api.AuthenticationMethodOIDC}, remote.AuthType) && (args.TLSClientCert == "" || args.TLSClientKey == "") {
		return nil, errors.New("Missing TLS client certificate and key")
	}

	var d incus.InstanceServer
	if remote.KeepAlive > 0 {
		d, err = c.handleKeepAlive(remote, name, args)
		if err != nil {
			// On proxy failure, just fallback to regular client.
			d, err = incus.ConnectIncus(remote.Addr, args)
			if err != nil {
				return nil, err
			}
		}
	} else {
		d, err = incus.ConnectIncus(remote.Addr, args)
		if err != nil {
			return nil, err
		}
	}

	if remote.Project != "" && remote.Project != "default" {
		d = d.UseProject(remote.Project)
	}

	if c.ProjectOverride != "" {
		d = d.UseProject(c.ProjectOverride)
	}

	return d, nil
}

// GetImageServer returns a ImageServer struct for the remote.
func (c *Config) GetImageServer(name string) (incus.ImageServer, error) {
	// Handle "local" on non-Linux
	if name == "local" && runtime.GOOS != "linux" {
		return nil, ErrNotLinux
	}

	// Get the remote
	remote, ok := c.Remotes[name]
	if !ok {
		return nil, fmt.Errorf("The remote \"%s\" doesn't exist", name)
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Add image cache if specified.
	if c.CacheDir != "" {
		args.CachePath = c.CacheDir
		args.CacheExpiry = 5 * time.Minute
	}

	// Unix socket
	remoteAddr, hasUnixPrefix := strings.CutPrefix(remote.Addr, "unix:")
	if hasUnixPrefix {
		d, err := incus.ConnectIncusUnix(strings.TrimPrefix(remoteAddr, "//"), args)
		if err != nil {
			return nil, err
		}

		if remote.Project != "" && remote.Project != "default" {
			d = d.UseProject(remote.Project)
		}

		if c.ProjectOverride != "" {
			d = d.UseProject(c.ProjectOverride)
		}

		return d, nil
	}

	// HTTPs (simplestreams)
	if remote.Protocol == "simplestreams" {
		d, err := incus.ConnectSimpleStreams(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (OCI)
	if remote.Protocol == "oci" {
		// Handle credentials helper.
		if remote.CredHelper != "" {
			// Parse the URL.
			u, err := url.Parse(remote.Addr)
			if err != nil {
				return nil, err
			}

			// Call the helper.
			var stdout bytes.Buffer

			err = subprocess.RunCommandWithFds(
				context.TODO(),
				strings.NewReader(fmt.Sprintf("%s://%s", u.Scheme, u.Host)),
				&stdout,
				remote.CredHelper,
				"get")
			if err != nil {
				return nil, err
			}

			// Parse credential helper response.
			var res map[string]string
			err = json.Unmarshal(stdout.Bytes(), &res)
			if err != nil {
				return nil, err
			}

			// Update the URL to include the credentials.
			u.User = url.UserPassword(res["Username"], res["Secret"])
			remote.Addr = u.String()
		}

		d, err := incus.ConnectOCI(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (public)
	if remote.Public {
		d, err := incus.ConnectPublicIncus(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (private)
	d, err := incus.ConnectIncus(remote.Addr, args)
	if err != nil {
		return nil, err
	}

	if remote.Project != "" && remote.Project != "default" {
		d = d.UseProject(remote.Project)
	}

	if c.ProjectOverride != "" {
		d = d.UseProject(c.ProjectOverride)
	}

	return d, nil
}

// getConnectionArgs retrieves the connection arguments for the specified remote.
// It constructs the necessary connection arguments based on the remote's configuration, including authentication type,
// authentication interactors, cookie jar, OIDC tokens, TLS certificates, and client key.
// The function returns the connection arguments or an error if any configuration is missing or encounters a problem.
func (c *Config) getConnectionArgs(name string) (*incus.ConnectionArgs, error) {
	remote := c.Remotes[name]
	args := incus.ConnectionArgs{
		UserAgent: c.UserAgent,
		AuthType:  remote.AuthType,
	}

	if args.AuthType == api.AuthenticationMethodOIDC {
		if c.oidcTokens == nil {
			c.oidcTokens = map[string]*oidc.Tokens[*oidc.IDTokenClaims]{}
		}

		tokenPath := c.OIDCTokenPath(name)

		if c.oidcTokens[name] == nil {
			if util.PathExists(tokenPath) {
				content, err := os.ReadFile(tokenPath)
				if err != nil {
					return nil, err
				}

				var tokens oidc.Tokens[*oidc.IDTokenClaims]

				err = json.Unmarshal(content, &tokens)
				if err != nil {
					return nil, err
				}

				c.oidcTokens[name] = &tokens
			} else {
				c.oidcTokens[name] = &oidc.Tokens[*oidc.IDTokenClaims]{}
			}
		}

		args.OIDCTokens = c.oidcTokens[name]
	}

	// Stop here if no TLS involved
	if strings.HasPrefix(remote.Addr, "unix:") {
		return &args, nil
	}

	// Server certificate
	if util.PathExists(c.ServerCertPath(name)) {
		content, err := os.ReadFile(c.ServerCertPath(name))
		if err != nil {
			return nil, err
		}

		args.TLSServerCert = string(content)
	}

	// Stop here if no client certificate involved
	if remote.Protocol != "incus" || slices.Contains([]string{api.AuthenticationMethodOIDC}, remote.AuthType) {
		return &args, nil
	}

	// Client certificate
	var err error

	args.TLSClientCert, args.TLSClientKey, args.TLSCA, err = c.GetClientCertificate(name)
	if err != nil {
		return nil, err
	}

	return &args, nil
}
