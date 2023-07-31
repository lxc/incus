// LXD external REST API
//
// This is the REST API used by all LXD clients.
// Internal endpoints aren't included in this documentation.
//
// The LXD API is available over both a local unix+http and remote https API.
// Authentication for local users relies on group membership and access to the unix socket.
// For remote users, the default authentication method is TLS client
// certificates with a macaroon based (candid) authentication method also
// supported.
//
//	Version: 1.0
//	License: Apache-2.0 https://www.apache.org/licenses/LICENSE-2.0
//	Contact: LXD upstream <lxd@lists.canonical.com> https://github.com/canonical/lxd
//
// swagger:meta
package main

// Common error definitions.
