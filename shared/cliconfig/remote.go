package cliconfig

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/zitadel/oidc/v2/pkg/oidc"
	"golang.org/x/crypto/ssh"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// Remote holds details for communication with a remote daemon.
type Remote struct {
	Addr      string `yaml:"addr"`
	AuthType  string `yaml:"auth_type,omitempty"`
	KeepAlive int    `yaml:"keepalive,omitempty"`
	Project   string `yaml:"project,omitempty"`
	Protocol  string `yaml:"protocol,omitempty"`
	Public    bool   `yaml:"public"`
	Global    bool   `yaml:"-"`
	Static    bool   `yaml:"-"`
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
	if remote.Public || remote.Protocol == "simplestreams" {
		return nil, fmt.Errorf("The remote isn't a private server")
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Unix socket
	if strings.HasPrefix(remote.Addr, "unix:") {
		d, err := incus.ConnectIncusUnix(strings.TrimPrefix(strings.TrimPrefix(remote.Addr, "unix:"), "//"), args)
		if err != nil {
			var netErr *net.OpError

			if errors.As(err, &netErr) {
				errMsg := netErr.Unwrap().Error()
				if errMsg == "connect: connection refused" || errMsg == "connect: no such file or directory" {
					return nil, fmt.Errorf("The incus daemon doesn't appear to be started (socket path: %s)", netErr.Addr)
				} else if errMsg == "connect: permission denied" {
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
		return nil, fmt.Errorf("Missing TLS client certificate and key")
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

	// Unix socket
	if strings.HasPrefix(remote.Addr, "unix:") {
		d, err := incus.ConnectIncusUnix(strings.TrimPrefix(strings.TrimPrefix(remote.Addr, "unix:"), "//"), args)
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
	if remote.Protocol == "simplestreams" || slices.Contains([]string{api.AuthenticationMethodOIDC}, remote.AuthType) {
		return &args, nil
	}

	// Certificate paths.
	var pathClientCertificate string
	var pathClientKey string
	var pathClientCA string
	if c.HasRemoteClientCertificate(name) {
		pathClientCertificate = c.ConfigPath("clientcerts", fmt.Sprintf("%s.crt", name))
		pathClientKey = c.ConfigPath("clientcerts", fmt.Sprintf("%s.key", name))
		pathClientCA = c.ConfigPath("clientcerts", fmt.Sprintf("%s.ca", name))
	} else {
		pathClientCertificate = c.ConfigPath("client.crt")
		pathClientKey = c.ConfigPath("client.key")
		pathClientCA = c.ConfigPath("client.ca")
	}

	// Client certificate
	if util.PathExists(pathClientCertificate) {
		content, err := os.ReadFile(pathClientCertificate)
		if err != nil {
			return nil, err
		}

		args.TLSClientCert = string(content)
	}

	// Client CA
	if util.PathExists(pathClientCA) {
		content, err := os.ReadFile(pathClientCA)
		if err != nil {
			return nil, err
		}

		args.TLSCA = string(content)
	}

	// Client key
	if util.PathExists(pathClientKey) {
		content, err := os.ReadFile(pathClientKey)
		if err != nil {
			return nil, err
		}

		pemKey, _ := pem.Decode(content)
		// Golang has deprecated all methods relating to PEM encryption due to a vulnerability.
		// However, the weakness does not make PEM unsafe for our purposes as it pertains to password protection on the
		// key file (client.key is only readable to the user in any case), so we'll ignore deprecation.
		isEncrypted := x509.IsEncryptedPEMBlock(pemKey) //nolint:staticcheck
		isSSH := pemKey.Type == "OPENSSH PRIVATE KEY"
		if isEncrypted || isSSH {
			if c.PromptPassword == nil {
				return nil, fmt.Errorf("Private key is password protected and no helper was configured")
			}

			password, err := c.PromptPassword(pathClientKey)
			if err != nil {
				return nil, err
			}

			if isSSH {
				sshKey, err := ssh.ParseRawPrivateKeyWithPassphrase(content, []byte(password))
				if err != nil {
					return nil, err
				}

				ecdsaKey, okEcdsa := (sshKey).(*ecdsa.PrivateKey)
				rsaKey, okRsa := (sshKey).(*rsa.PrivateKey)
				if okEcdsa {
					derKey, err := x509.MarshalECPrivateKey(ecdsaKey)
					if err != nil {
						return nil, err
					}

					content = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derKey})
				} else if okRsa {
					derKey := x509.MarshalPKCS1PrivateKey(rsaKey)
					content = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derKey})
				} else {
					return nil, fmt.Errorf("Unsupported key type: %T", sshKey)
				}
			} else {
				derKey, err := x509.DecryptPEMBlock(pemKey, []byte(password)) //nolint:staticcheck
				if err != nil {
					return nil, err
				}

				content = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derKey})
			}
		}

		args.TLSClientKey = string(content)
	}

	return &args, nil
}
