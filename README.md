# Incus

Incus is a modern, secure and powerful system container and virtual machine manager.

<!-- Include start Incus intro -->

It provides a unified experience for running and managing full Linux systems inside containers or virtual machines. Incus supports images for a large number of Linux distributions (official Ubuntu images and images provided by the community) and is built around a very powerful, yet pretty simple, REST API. Incus scales from one instance on a single machine to a cluster in a full data center rack, making it suitable for running workloads both for development and in production.

Incus allows you to easily set up a system that feels like a small private cloud. You can run any type of workload in an efficient way while keeping your resources optimized.

You should consider using Incus if you want to containerize different environments or run virtual machines, or in general run and manage your infrastructure in a cost-effective way.


<!-- Include end Incus intro -->

## Fork of Canonical LXD
Incus, which is named after the [Cumulonimbus incus](https://en.wikipedia.org/wiki/Cumulonimbus_incus) or anvil cloud is a community fork of Canonical's LXD.

This fork was made in response to [Canonical's takeover](https://linuxcontainers.org/lxd/) of the LXD project from the Linux Containers community.

The main aim of this fork is to provide once again a real community project where everyone's contributions are welcome and no one single commercial entity is in charge of the project.

The fork was done at the LXD 5.16 release, making it possible to upgrade from LXD releases up to and including LXD 5.16.
Upgrading from a later LXD release may not work as the two projects are likely to start diverging from this point onwards.

Incus will keep monitoring and importing relevant changes from LXD over time though changes and features that are specific to Ubuntu or Canonical's products are unlikely to be carried over.

## Get started

This is still the very early days of this fork. No packages or even releases currently exist.
For production use, you are likely better off sticking with Canonical's LXD for the time being until stable release of Incus exist.

You can test the current state of Incus through an online session here: https://linuxcontainers.org/incus/try-it/

## Status

Type                | Service               | Status
---                 | ---                   | ---
CI (client)         | GitHub                | [![Build Status](https://github.com/lxc/incus/workflows/Client%20build%20and%20unit%20tests/badge.svg)](https://github.com/lxc/incus/actions)
CI (server)         | GitHub                | [![Build Status](https://github.com/lxc/incus/workflows/Tests/badge.svg)](https://github.com/lxc/incus/actions)
Go documentation    | Godoc                 | [![GoDoc](https://godoc.org/github.com/lxc/incus/client?status.svg)](https://godoc.org/github.com/lxc/incus/client)
Static analysis     | GoReport              | [![Go Report Card](https://goreportcard.com/badge/github.com/lxc/incus)](https://goreportcard.com/report/github.com/lxc/incus)

## Security

<!-- Include start security -->

Consider the following aspects to ensure that your Incus installation is secure:

- Keep your operating system up-to-date and install all available security patches.
- Use only supported Incus versions.
- Restrict access to the Incus daemon and the remote API.
- Do not use privileged containers unless required. If you use privileged containers, put appropriate security measures in place. See the [LXC security page](https://linuxcontainers.org/lxc/security/) for more information.
- Configure your network interfaces to be secure.
<!-- Include end security -->

See [Security](https://github.com/lxc/incus/blob/main/doc/explanation/security.md) for detailed information.

**IMPORTANT:**
<!-- Include start security note -->
Local access to Incus through the Unix socket always grants full access to Incus.
This includes the ability to attach file system paths or devices to any instance as well as tweak the security features on any instance.

Therefore, you should only give such access to users who you'd trust with root access to your system.
<!-- Include end security note -->
<!-- Include start support -->

## Support and community

The following channels are available for you to interact with the Incus community.

### Bug reports

You can file bug reports and feature requests at: [`https://github.com/lxc/incus/issues/new`](https://github.com/lxc/incus/issues/new)

## Documentation

The official documentation is available at: [`https://github.com/lxc/incus/tree/main/doc`](https://github.com/lxc/incus/tree/main/doc)

<!-- Include end support -->

## Contributing

Fixes and new features are greatly appreciated. Make sure to read our [contributing guidelines](CONTRIBUTING.md) first!
