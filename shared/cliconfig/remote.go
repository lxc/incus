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

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

// RemoteTLS holds the content of the TLS certificate, key and CA.
// This is primarily meant for use by the keepalive proxy.
type RemoteTLS struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
	CA          string `json:"ca"`
}

// Remote holds details for communication with a remote daemon.
type Remote struct {
	Addrs           []string   `yaml:"-"`
	LastWorkingAddr string     `yaml:"last_working_address,omitempty"`
	AuthType        string     `yaml:"auth_type,omitempty"`
	KeepAlive       int        `yaml:"keepalive,omitempty"`
	Project         string     `yaml:"project,omitempty"`
	Protocol        string     `yaml:"protocol,omitempty"`
	CredHelper      string     `yaml:"credentials_helper,omitempty"`
	Public          bool       `yaml:"public"`
	Global          bool       `yaml:"-"`
	Static          bool       `yaml:"-"`
	TLS             *RemoteTLS `yaml:"-"`
}

// MarshalYAML overrides the way the Remote struct is marshalled.
func (r Remote) MarshalYAML() (any, error) {
	type R Remote
	return struct {
		*R    `yaml:",inline"`
		Addrs string `yaml:"addr"`
	}{
		R:     (*R)(&r),
		Addrs: strings.Join(r.Addrs, ","),
	}, nil
}

// UnmarshalYAML overrides the way the Remote struct is unmarshalled.
func (r *Remote) UnmarshalYAML(unmarshal func(any) error) error {
	type R Remote
	tmp := struct {
		*R    `yaml:",inline"`
		Addrs string `yaml:"addr"`
	}{
		R: (*R)(r),
	}

	err := unmarshal(&tmp)
	if err != nil {
		return err
	}

	r.Addrs = util.SplitNTrimSpace(tmp.Addrs, ",", -1, true)
	if r.Addrs == nil {
		return errors.New("Remotes must have at least one address")
	}

	return nil
}

// RollingAddrs allows iterating over the set of addresses starting with the last known working one.
func (r *Remote) RollingAddrs() <-chan string {
	ch := make(chan string)

	go func() {
		defer close(ch)
		start := 0
		if r.LastWorkingAddr != "" {
			for i, addr := range r.Addrs {
				if addr == r.LastWorkingAddr {
					start = i
					break
				}
			}
		}

		for i := 0; i < len(r.Addrs); i++ {
			ch <- r.Addrs[(start+i)%len(r.Addrs)]
		}
	}()

	return ch
}

// ParseRemote splits remote and object.
func (c *Config) ParseRemote(raw string) (string, string, error) {
	result := strings.SplitN(raw, ":", 2)
	// If the remote contains `=`, it this definitely is NOT a remote.
	if len(result) == 1 || strings.Contains(result[0], "=") {
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

func (c *Config) getInstanceServer(args *incus.ConnectionArgs, remote Remote, addr string) (incus.InstanceServer, error) {
	// Unix socket
	remoteAddr, hasUnixPrefix := strings.CutPrefix(addr, "unix:")
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

	// Connect to Incus.
	d, err := incus.ConnectIncus(addr, args)
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

	// See if we can shortcut things through the keepalive daemon.
	if remote.KeepAlive > 0 {
		d, err := c.handleKeepAlive(remote, name)
		if err == nil {
			// Apply the project config
			if remote.Project != "" && remote.Project != "default" {
				d = d.UseProject(remote.Project)
			}

			if c.ProjectOverride != "" {
				d = d.UseProject(c.ProjectOverride)
			}

			// Return the connection to the keepalive proxy.
			return d, nil
		}
	}

	var errs []error

	for addr := range remote.RollingAddrs() {
		// Get connection arguments
		args, err := c.getConnectionArgs(name, addr)
		if err != nil {
			if len(remote.Addrs) == 1 {
				return nil, err
			}

			errs = append(errs, err)
			continue
		}

		d, err := c.getInstanceServer(args, remote, addr)
		if err != nil {
			if len(remote.Addrs) == 1 {
				return nil, err
			}

			errs = append(errs, err)
			continue
		}

		return d, nil
	}

	return nil, errors.Join(errs...)
}

func (c *Config) getImageServer(args *incus.ConnectionArgs, remote Remote, addr string) (incus.ImageServer, error) {
	// Unix socket
	remoteAddr, hasUnixPrefix := strings.CutPrefix(addr, "unix:")
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
		d, err := incus.ConnectSimpleStreams(addr, args)
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
			u, err := url.Parse(addr)
			if err != nil {
				return nil, err
			}

			// Call the helper.
			var stdout bytes.Buffer

			err = subprocess.RunCommandWithFds(
				context.TODO(),
				strings.NewReader(u.Host),
				&stdout,
				remote.CredHelper,
				"get",
			)
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
			addr = u.String()
		}

		d, err := incus.ConnectOCI(addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (public)
	if remote.Public {
		d, err := incus.ConnectPublicIncus(addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (private)
	d, err := incus.ConnectIncus(addr, args)
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

	var errs []error

	for addr := range remote.RollingAddrs() {
		// Get connection arguments
		args, err := c.getConnectionArgs(name, addr)
		if err != nil {
			if len(remote.Addrs) == 1 {
				return nil, err
			}

			errs = append(errs, err)
			continue
		}

		// Add image cache if specified.
		if c.CacheDir != "" {
			args.CachePath = c.CacheDir
			args.CacheExpiry = 5 * time.Minute
		}

		d, err := c.getImageServer(args, remote, addr)
		if err != nil {
			if len(remote.Addrs) == 1 {
				return nil, err
			}

			errs = append(errs, err)
			continue
		}

		return d, nil
	}

	return nil, errors.Join(errs...)
}

// getConnectionArgs retrieves the connection arguments for the specified remote.
// It constructs the necessary connection arguments based on the remote's configuration, including authentication type,
// authentication interactors, cookie jar, OIDC tokens, TLS certificates, and client key.
// The function returns the connection arguments or an error if any configuration is missing or encounters a problem.
func (c *Config) getConnectionArgs(name string, addr string) (*incus.ConnectionArgs, error) {
	remote := c.Remotes[name]
	args := incus.ConnectionArgs{
		UserAgent: c.UserAgent,
		AuthType:  remote.AuthType,
	}

	if args.AuthType == api.AuthenticationMethodOIDC {
		// Lock to prevent concurrent access/changes to oidcTokens.
		c.mu.Lock()
		defer c.mu.Unlock()

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
	if strings.HasPrefix(addr, "unix:") {
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
