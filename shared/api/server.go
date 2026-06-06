package api

import (
	"strings"

	"go.yaml.in/yaml/v4"
)

// ServerEnvironment represents the read-only environment fields of a server configuration.
type ServerEnvironment struct {
	// List of addresses the server is listening on
	// Example: [":8443"]
	Addresses []string `json:"addresses" yaml:"addresses"`

	// List of architectures supported by the server
	// Example: ["x86_64", "i686"]
	Architectures []string `json:"architectures" yaml:"architectures"`

	// Server certificate as PEM encoded X509
	// Example: X509 PEM certificate
	Certificate string `json:"certificate" yaml:"certificate"`

	// Server certificate fingerprint as SHA256
	// Example: fd200419b271f1dc2a5591b693cc5774b7f234e1ff8c6b78ad703b6888fe2b69
	CertificateFingerprint string `json:"certificate_fingerprint" yaml:"certificate_fingerprint"`

	// List of supported instance drivers (separate by " | ")
	// Example: lxc | qemu
	Driver string `json:"driver" yaml:"driver"`

	// List of supported instance driver versions (separate by " | ")
	// Example: 4.0.7 | 5.2.0
	DriverVersion string `json:"driver_version" yaml:"driver_version"`

	// Current firewall driver
	// Example: nftables
	//
	// API extension: firewall_driver
	Firewall string `json:"firewall" yaml:"firewall"`

	// OS kernel name
	// Example: Linux
	Kernel string `json:"kernel" yaml:"kernel"`

	// OS kernel architecture
	// Example: x86_64
	KernelArchitecture string `json:"kernel_architecture" yaml:"kernel_architecture"`

	// Map of kernel features that were tested on startup
	// Example: {"netnsid_getifaddrs": "true", "seccomp_listener": "true"}
	//
	// API extension: kernel_features
	KernelFeatures map[string]string `json:"kernel_features" yaml:"kernel_features"`

	// Kernel version
	// Example: 5.4.0-36-generic
	KernelVersion string `json:"kernel_version" yaml:"kernel_version"`

	// Map of LXC features that were tested on startup
	// Example: {"cgroup2": "true", "devpts_fd": "true", "pidfd": "true"}
	//
	// API extension: lxc_features
	LXCFeatures map[string]string `json:"lxc_features" yaml:"lxc_features"`

	// Name of the operating system (Linux distribution)
	// Example: Ubuntu
	//
	// API extension: api_os
	OSName string `json:"os_name" yaml:"os_name"`

	// Version of the operating system (Linux distribution)
	// Example: 22.04
	//
	// API extension: api_os
	OSVersion string `json:"os_version" yaml:"os_version"`

	// Current project name
	// Example: default
	//
	// API extension: projects
	Project string `json:"project" yaml:"project"`

	// Server implementation name
	// Example: incus
	Server string `json:"server" yaml:"server"`

	// Whether the server is part of a cluster
	// Example: false
	//
	// API extension: clustering
	ServerClustered bool `json:"server_clustered" yaml:"server_clustered"`

	// Mode that the event distribution subsystem is operating in on this server.
	// Either "full-mesh", "hub-server" or "hub-client".
	// Example: full-mesh
	//
	// API extension: event_hub
	ServerEventMode string `json:"server_event_mode" yaml:"server_event_mode"`

	// Server hostname
	// Example: castiana
	//
	// API extension: clustering
	ServerName string `json:"server_name" yaml:"server_name"`

	// PID of the daemon
	// Example: 1453969
	ServerPid int `json:"server_pid" yaml:"server_pid"`

	// Server version
	// Example: 4.11
	ServerVersion string `json:"server_version" yaml:"server_version"`

	// List of active storage drivers (separate by " | ")
	// Example: dir | zfs
	Storage string `json:"storage" yaml:"storage"`

	// List of active storage driver versions (separate by " | ")
	// Example: 1 | 0.8.4-1ubuntu11
	StorageVersion string `json:"storage_version" yaml:"storage_version"`

	// List of supported storage drivers
	StorageSupportedDrivers []ServerStorageDriverInfo `json:"storage_supported_drivers" yaml:"storage_supported_drivers"`
}

// ServerStorageDriverInfo represents the read-only info about a storage driver
//
// swagger:model
//
// API extension: server_supported_storage_drivers.
type ServerStorageDriverInfo struct {
	// Name of the driver
	// Example: zfs
	//
	// API extension: server_supported_storage_drivers
	Name string

	// Version of the driver
	// Example: 0.8.4-1ubuntu11
	//
	// API extension: server_supported_storage_drivers
	Version string

	// Whether the driver has remote volumes
	// Example: false
	//
	// API extension: server_supported_storage_drivers
	Remote bool
}

// ServerPut represents the modifiable fields of a server configuration
//
// swagger:model
type ServerPut struct {
	// Server configuration map (refer to doc/server.md)
	// Example: {"core.https_address": ":8443"}
	Config ConfigMap `json:"config" yaml:"config"`
}

// ServerUntrusted represents a server configuration for an untrusted client
//
// swagger:model
type ServerUntrusted struct {
	ServerPut `yaml:",inline"`

	// List of supported API extensions
	// Read only: true
	// Example: ["etag", "patch", "network", "storage"]
	APIExtensions []string `json:"api_extensions" yaml:"api_extensions,omitempty"`

	// Support status of the current API (one of "devel", "stable" or "deprecated")
	// Read only: true
	// Example: stable
	APIStatus string `json:"api_status" yaml:"api_status"`

	// API version number
	// Read only: true
	// Example: 1.0
	APIVersion string `json:"api_version" yaml:"api_version"`

	// Whether the client is trusted (one of "trusted" or "untrusted")
	// Read only: true
	// Example: untrusted
	Auth string `json:"auth" yaml:"auth"`

	// Whether the server is public-only (only public endpoints are implemented)
	// Read only: true
	// Example: false
	Public bool `json:"public" yaml:"public"`

	// List of supported authentication methods
	// Read only: true
	// Example: ["tls"]
	//
	// API extension: macaroon_authentication
	AuthMethods []string `json:"auth_methods" yaml:"auth_methods"`
}

// Server represents a server configuration
//
// swagger:model
type Server struct {
	ServerUntrusted `yaml:",inline"`

	// The current API user identifier
	// Read only: true
	// Example: uid=201105
	//
	// API extension: auth_user
	AuthUserName string `json:"auth_user_name" yaml:"auth_user_name"`

	// The current API user login method
	// Read only: true
	// Example: unix
	//
	// API extension: auth_user
	AuthUserMethod string `json:"auth_user_method" yaml:"auth_user_method"`

	// Read-only status/configuration information
	// Read only: true
	Environment ServerEnvironment `json:"environment" yaml:"environment"`
}

// Writable converts a full Server struct into a ServerPut struct (filters read-only fields).
func (srv *Server) Writable() ServerPut {
	return srv.ServerPut
}

// isConfigKeySensitive is used to check if a given configuration key is likely to be sensitive and should be censored.
func isConfigKeySensitive(key string) bool {
	fields := strings.Split(key, ".")
	segment := fields[len(fields)-1]

	for _, suffix := range []string{"key", "cert", "password", "secret", "token"} {
		if strings.HasSuffix(segment, suffix) {
			return true
		}
	}

	return false
}

// ServerFiltered represents a Server with all sensitive information removed and some other noisy attributes cleared out.
type ServerFiltered struct {
	Server `yaml:",inline"`

	// Number of supported API extensions
	// Example: 412
	APIExtensionsCount int `json:"api_extensions_count"`
}

// MarshalYAML is used as a workaround to keep the field ordering the same in the output by injecting api_extensions_count at the right spot.
func (srv ServerFiltered) MarshalYAML() (any, error) {
	node := yaml.Node{}

	err := node.Encode(srv.Server)
	if err != nil {
		return nil, err
	}

	if srv.APIExtensionsCount > 0 {
		count := yaml.Node{}
		err = count.Encode(srv.APIExtensionsCount)
		if err != nil {
			return nil, err
		}

		key := yaml.Node{Kind: yaml.ScalarNode, Value: "api_extensions_count"}

		// Insert just before "api_status", matching the spot of the API extension list.
		idx := len(node.Content)
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == "api_status" {
				idx = i
				break
			}
		}

		content := make([]*yaml.Node, 0, len(node.Content)+2)
		content = append(content, node.Content[:idx]...)
		content = append(content, &key, &count)
		content = append(content, node.Content[idx:]...)
		node.Content = content
	}

	return &node, nil
}

// Filtered converts Server to ServerFiltered by filtering out the sensitive config keys and summarizing some of the data.
func (srv *Server) Filtered() *ServerFiltered {
	filtered := ServerFiltered{Server: *srv}

	// Drop the full server certificate (we keep the fingerprint).
	filtered.Environment.Certificate = ""

	// Replace the full API extension list with a count.
	filtered.APIExtensionsCount = len(srv.APIExtensions)
	filtered.APIExtensions = nil

	// Censor any sensitive configuration value.
	if srv.Config != nil {
		filtered.Config = make(ConfigMap, len(srv.Config))
		for k, v := range srv.Config {
			if v != "" && isConfigKeySensitive(k) {
				filtered.Config[k] = "SENSITIVE"
				continue
			}

			filtered.Config[k] = v
		}
	}

	return &filtered
}
