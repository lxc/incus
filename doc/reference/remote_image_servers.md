---
discourse: 16647
relatedlinks: https://www.youtube.com/watch?v=pM0EgUqj2a0
---

(remote-image-servers)=
# Remote image servers

The [`lxc`](incus.md) CLI command comes pre-configured with the following default remote image servers:

`ubuntu:`
: This server provides official stable Ubuntu images.
  All images are cloud images, which means that they include both `cloud-init` and the `lxd-agent`.

  See [`cloud-images.ubuntu.com/releases`](https://cloud-images.ubuntu.com/releases/) for an overview of available images.

`ubuntu-daily:`
: This server provides official daily Ubuntu images.
  All images are cloud images, which means that they include both `cloud-init` and the `lxd-agent`.

  See [`cloud-images.ubuntu.com/daily`](https://cloud-images.ubuntu.com/daily/) for an overview of available images.

`images:`
: This server provides unofficial images for a variety of Linux distributions.
  The images are maintained by the [Linux Containers](https://linuxcontainers.org/) team and are built to be compact and minimal.

  See [`images.linuxcontainers.org`](https://images.linuxcontainers.org) for an overview of available images.

(remote-image-server-types)=
## Remote server types

LXD supports the following types of remote image servers:

Simple streams servers
: Pure image servers that use the [simple streams format](https://git.launchpad.net/simplestreams/tree/).
  The default image servers are simple streams servers.

Public LXD servers
: LXD servers that are used solely to serve images and do not run instances themselves.

  To make a LXD server publicly available over the network on port 8443, set the {config:option}`server-core:core.https_address` configuration option to `:8443` and do not configure any authentication methods (see {ref}`server-expose` for more information).
  Then set the images that you want to share to `public`.

LXD servers
: Regular LXD servers that you can manage over a network, and that can also be used as image servers.

  For security reasons, you should restrict the access to the remote API and configure an authentication method to control access.
  See {ref}`server-expose` and {ref}`authentication` for more information.
