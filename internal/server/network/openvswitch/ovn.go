package openvswitch

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/subprocess"
)

// NewOVN initialises new OVN client wrapper with the connection set in network.ovn.northbound_connection config.
func NewOVN(s *state.State) (*OVN, error) {
	// Get database connection strings.
	nbConnection := s.GlobalConfig.NetworkOVNNorthboundConnection()
	sbConnection, err := NewOVS().OVNSouthboundDBRemoteAddress()
	if err != nil {
		return nil, fmt.Errorf("Failed to get OVN southbound connection string: %w", err)
	}

	// Create the OVN struct.
	client := &OVN{
		nbDBAddr: nbConnection,
		sbDBAddr: sbConnection,
	}

	// If using SSL, then get the CA and client key pair.
	if strings.Contains(nbConnection, "ssl:") {
		sslCACert, sslClientCert, sslClientKey := s.GlobalConfig.NetworkOVNSSL()

		if sslCACert == "" {
			content, err := os.ReadFile("/etc/ovn/ovn-central.crt")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL CA certificate defined")
				}

				return nil, err
			}

			sslCACert = string(content)
		}

		if sslClientCert == "" {
			content, err := os.ReadFile("/etc/ovn/cert_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL client certificate defined")
				}

				return nil, err
			}

			sslClientCert = string(content)
		}

		if sslClientKey == "" {
			content, err := os.ReadFile("/etc/ovn/key_host")
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("OVN configured to use SSL but no SSL client key defined")
				}

				return nil, err
			}

			sslClientKey = string(content)
		}

		client.sslCACert = sslCACert
		client.sslClientCert = sslClientCert
		client.sslClientKey = sslClientKey
	}

	return client, nil
}

// OVN command wrapper.
type OVN struct {
	nbDBAddr string
	sbDBAddr string

	sslCACert     string
	sslClientCert string
	sslClientKey  string
}

// getNorthboundDB returns connection string to use for northbound database.
func (o *OVN) getNorthboundDB() string {
	if o.nbDBAddr == "" {
		return "unix:/var/run/ovn/ovnnb_db.sock"
	}

	return o.nbDBAddr
}

// getSouthboundDB returns connection string to use for northbound database.
func (o *OVN) getSouthboundDB() string {
	if o.sbDBAddr == "" {
		return "unix:/var/run/ovn/ovnsb_db.sock"
	}

	return o.sbDBAddr
}

// sbctl executes ovn-sbctl with arguments to connect to wrapper's southbound database.
func (o *OVN) sbctl(args ...string) (string, error) {
	return o.xbctl(true, args...)
}

// nbctl executes ovn-nbctl with arguments to connect to wrapper's northbound database.
func (o *OVN) nbctl(args ...string) (string, error) {
	return o.xbctl(false, append([]string{"--wait=sb"}, args...)...)
}

// xbctl optionally executes either ovn-nbctl or ovn-sbctl with arguments to connect to wrapper's northbound or southbound database.
func (o *OVN) xbctl(southbound bool, extraArgs ...string) (string, error) {
	dbAddr := o.getNorthboundDB()
	cmd := "ovn-nbctl"
	if southbound {
		dbAddr = o.getSouthboundDB()
		cmd = "ovn-sbctl"
	}

	// Figure out args.
	args := []string{"--timeout=10", "--db", dbAddr}

	// Handle SSL args.
	files := []*os.File{}
	if strings.Contains(dbAddr, "ssl:") {
		// Handle client certificate.
		clientCertFile, err := linux.CreateMemfd([]byte(o.sslClientCert))
		if err != nil {
			return "", err
		}

		defer clientCertFile.Close()
		files = append(files, clientCertFile)

		// Handle client key.
		clientKeyFile, err := linux.CreateMemfd([]byte(o.sslClientKey))
		if err != nil {
			return "", err
		}

		defer clientKeyFile.Close()
		files = append(files, clientKeyFile)

		// Handle CA certificate.
		caCertFile, err := linux.CreateMemfd([]byte(o.sslCACert))
		if err != nil {
			return "", err
		}

		defer caCertFile.Close()
		files = append(files, caCertFile)

		args = append(args,
			"-c", "/proc/self/fd/3",
			"-p", "/proc/self/fd/4",
			"-C", "/proc/self/fd/5",
		)
	}

	args = append(args, extraArgs...)
	return subprocess.RunCommandInheritFds(context.Background(), files, cmd, args...)
}
