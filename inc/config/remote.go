package config

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery/form"
	"github.com/juju/persistent-cookiejar"
	"github.com/zitadel/oidc/v2/pkg/oidc"
	schemaform "gopkg.in/juju/environschema.v1/form"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
)

// Remote holds details for communication with a remote daemon.
type Remote struct {
	Addr     string `yaml:"addr"`
	AuthType string `yaml:"auth_type,omitempty"`
	Domain   string `yaml:"domain,omitempty"`
	Project  string `yaml:"project,omitempty"`
	Protocol string `yaml:"protocol,omitempty"`
	Public   bool   `yaml:"public"`
	Global   bool   `yaml:"-"`
	Static   bool   `yaml:"-"`
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
		if shared.IsSnapshot(raw) && shared.IsSnapshot(result[0]) {
			return c.DefaultRemote, raw, nil
		}

		return "", "", fmt.Errorf("The remote \"%s\" doesn't exist", result[0])
	}

	return result[0], result[1], nil
}

// GetInstanceServer returns a InstanceServer struct for the remote.
func (c *Config) GetInstanceServer(name string) (lxd.InstanceServer, error) {
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
		return nil, fmt.Errorf("The remote isn't a private LXD server")
	}

	// Get connection arguments
	args, err := c.getConnectionArgs(name)
	if err != nil {
		return nil, err
	}

	// Unix socket
	if strings.HasPrefix(remote.Addr, "unix:") {
		d, err := lxd.ConnectLXDUnix(strings.TrimPrefix(strings.TrimPrefix(remote.Addr, "unix:"), "//"), args)
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

	// HTTPs
	if !shared.StringInSlice(remote.AuthType, []string{"candid", "oidc"}) && (args.TLSClientCert == "" || args.TLSClientKey == "") {
		return nil, fmt.Errorf("Missing TLS client certificate and key")
	}

	d, err := lxd.ConnectLXD(remote.Addr, args)
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
func (c *Config) GetImageServer(name string) (lxd.ImageServer, error) {
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
		d, err := lxd.ConnectLXDUnix(strings.TrimPrefix(strings.TrimPrefix(remote.Addr, "unix:"), "//"), args)
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
		d, err := lxd.ConnectSimpleStreams(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (public LXD)
	if remote.Public {
		d, err := lxd.ConnectPublicLXD(remote.Addr, args)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	// HTTPs (private LXD)
	d, err := lxd.ConnectLXD(remote.Addr, args)
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
func (c *Config) getConnectionArgs(name string) (*lxd.ConnectionArgs, error) {
	remote := c.Remotes[name]
	args := lxd.ConnectionArgs{
		UserAgent: c.UserAgent,
		AuthType:  remote.AuthType,
	}

	if args.AuthType == "candid" {
		args.AuthInteractor = []httpbakery.Interactor{
			form.Interactor{Filler: schemaform.IOFiller{}},
			httpbakery.WebBrowserInteractor{
				OpenWebBrowser: func(uri *url.URL) error {
					if remote.Domain != "" {
						query := uri.Query()
						query.Set("domain", remote.Domain)
						uri.RawQuery = query.Encode()
					}

					return httpbakery.OpenWebBrowser(uri)
				},
			},
		}

		if c.cookieJars == nil || c.cookieJars[name] == nil {
			if !shared.PathExists(c.ConfigPath("jars")) {
				err := os.MkdirAll(c.ConfigPath("jars"), 0700)
				if err != nil {
					return nil, err
				}
			}

			if !shared.PathExists(c.CookiesPath(name)) {
				if shared.PathExists(c.ConfigPath("cookies")) {
					err := shared.FileCopy(c.ConfigPath("cookies"), c.CookiesPath(name))
					if err != nil {
						return nil, err
					}
				}
			}

			jar, err := cookiejar.New(
				&cookiejar.Options{
					Filename: c.CookiesPath(name),
				})
			if err != nil {
				return nil, err
			}

			if c.cookieJars == nil {
				c.cookieJars = map[string]*cookiejar.Jar{}
			}

			c.cookieJars[name] = jar
		}

		args.CookieJar = c.cookieJars[name]
	} else if args.AuthType == "oidc" {
		if c.oidcTokens == nil {
			c.oidcTokens = map[string]*oidc.Tokens[*oidc.IDTokenClaims]{}
		}

		tokenPath := c.OIDCTokenPath(name)

		if c.oidcTokens[name] == nil {
			if shared.PathExists(tokenPath) {
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
	if shared.PathExists(c.ServerCertPath(name)) {
		content, err := os.ReadFile(c.ServerCertPath(name))
		if err != nil {
			return nil, err
		}

		args.TLSServerCert = string(content)
	}

	// Stop here if no client certificate involved
	if remote.Protocol == "simplestreams" || shared.StringInSlice(remote.AuthType, []string{"candid", "oidc"}) {
		return &args, nil
	}

	// Client certificate
	if shared.PathExists(c.ConfigPath("client.crt")) {
		content, err := os.ReadFile(c.ConfigPath("client.crt"))
		if err != nil {
			return nil, err
		}

		args.TLSClientCert = string(content)
	}

	// Client CA
	if shared.PathExists(c.ConfigPath("client.ca")) {
		content, err := os.ReadFile(c.ConfigPath("client.ca"))
		if err != nil {
			return nil, err
		}

		args.TLSCA = string(content)
	}

	// Client key
	if shared.PathExists(c.ConfigPath("client.key")) {
		content, err := os.ReadFile(c.ConfigPath("client.key"))
		if err != nil {
			return nil, err
		}

		pemKey, _ := pem.Decode(content)
		// Golang has deprecated all methods relating to PEM encryption due to a vulnerability.
		// However, the weakness does not make PEM unsafe for our purposes as it pertains to password protection on the
		// key file (client.key is only readable to the user in any case), so we'll ignore deprecation.
		if x509.IsEncryptedPEMBlock(pemKey) { //nolint:staticcheck
			if c.PromptPassword == nil {
				return nil, fmt.Errorf("Private key is password protected and no helper was configured")
			}

			password, err := c.PromptPassword("client.crt")
			if err != nil {
				return nil, err
			}

			derKey, err := x509.DecryptPEMBlock(pemKey, []byte(password)) //nolint:staticcheck
			if err != nil {
				return nil, err
			}

			content = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: derKey})
		}

		args.TLSClientKey = string(content)
	}

	return &args, nil
}
