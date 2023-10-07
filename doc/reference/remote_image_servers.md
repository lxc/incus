(remote-image-servers)=
# Remote image servers

The [`incus`](incus.md) CLI command comes pre-configured with the following default remote image servers:

`images:`
: This server provides unofficial images for a variety of Linux distributions.
  The images are maintained by the [Linux Containers](https://linuxcontainers.org/) team and are built to be compact and minimal.

  See [`images.linuxcontainers.org`](https://images.linuxcontainers.org) for an overview of available images.

(remote-image-server-types)=
## Remote server types

Incus supports the following types of remote image servers:

Simple streams servers
: Pure image servers that use the [simple streams format](https://git.launchpad.net/simplestreams/tree/).
  The default image servers are simple streams servers.

Public Incus servers
: Incus servers that are used solely to serve images and do not run instances themselves.

  To make a Incus server publicly available over the network on port 8443, set the {config:option}`server-core:core.https_address` configuration option to `:8443` and do not configure any authentication methods (see {ref}`server-expose` for more information).
  Then set the images that you want to share to `public`.

Incus servers
: Regular Incus servers that you can manage over a network, and that can also be used as image servers.

  For security reasons, you should restrict the access to the remote API and configure an authentication method to control access.
  See {ref}`server-expose` and {ref}`authentication` for more information.
