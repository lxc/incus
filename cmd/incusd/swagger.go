// Incus external REST API
//
// This is the REST API used by all Incus clients.
// Internal endpoints aren't included in this documentation.
//
// The Incus API is available over both a local unix+http and remote https API.
// Authentication for local users relies on group membership and access to the unix socket.
// For remote users, the default authentication method is TLS client
// certificates.
//
//	Version: 1.0
//	License: Apache-2.0 https://www.apache.org/licenses/LICENSE-2.0
//	Contact: Incus upstream <lxc-devel@lists.linuxcontainers.org> https://github.com/lxc/incus
//
// swagger:meta
package main

// Common error definitions.
