(image-servers)=
# Default image server

The [`incus`](incus.md) CLI command comes pre-configured with the following default remote image server:

`images:`
: This server provides unofficial images for a variety of Linux distributions.
  The images are maintained by the [Linux Containers](https://linuxcontainers.org/) team and are built to be compact and minimal.

  See [`images.linuxcontainers.org`](https://images.linuxcontainers.org) for an overview of available images.

Additional image servers can be added through `incus remote add`.

(image-server-types)=
## Image server types

Incus supports the following types of remote image servers:

Simple streams servers
: Pure image servers that use the [simple streams format](https://git.launchpad.net/simplestreams/tree/).
  No special software is required to run such a server as it's only made of static files.
  The default `images:` server uses simplestreams.

Public Incus servers
: Incus servers that are used solely to serve images and do not run instances themselves.

  To make an Incus server publicly available over the network on port 8443, set the {config:option}`server-core:core.https_address` configuration option to `:8443` and do not configure any authentication methods (see {ref}`server-expose` for more information).
  Then set the images that you want to share to `public`.

Incus servers
: Regular Incus servers that you can manage over a network, and that can also be used as image servers.

  For security reasons, you should restrict the access to the remote API and configure an authentication method to control access.
  See {ref}`server-expose` and {ref}`authentication` for more information.

(image-server-tooling)=
## Tooling to manage a simplestreams server
Incus includes a tool called `incus-simplestreams` which can be used to manage a file system tree using the Simple streams format.

It supports importing either a container (`squashfs`) or virtual-machine (`qcow2`) image
with `incus-simplestreams add`, list all images available as well as their fingerprints
with `incus-simplestreams list` and remove images from the server with `incus-simplestreams remove`.

That file system tree must then be placed on a regular web server which supports HTTPS with a valid certificate.

When importing an image that doesn't come with an Incus metadata tarball, the `incus-simplestreams generate-metadata` command
can be used to generate a new basic metadata tarball from a few questions.
