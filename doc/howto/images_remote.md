(images-remote)=
# How to use remote images

The [`incus`](incus.md) CLI command is pre-configured with several remote image servers.
See {ref}`remote-image-servers` for an overview.

## List configured remotes

<!-- Include start list remotes -->
To see all configured remote servers, enter the following command:

    incus remote list

Remote servers that use the [simple streams format](https://git.launchpad.net/simplestreams/tree/) are pure image servers.
Servers that use the `incus` format are Incus servers, which either serve solely as image servers or might provide some images in addition to serving as regular Incus servers.
See {ref}`remote-image-server-types` for more information.
<!-- Include end list remotes -->

## List available images on a remote

To list all remote images on a server, enter the following command:

    incus image list <remote>:

You can filter the results.
See {ref}`images-manage-filter` for instructions.

## Add a remote server

How to add a remote depends on the protocol that the server uses.

### Add a simple streams server

To add a simple streams server as a remote, enter the following command:

    incus remote add <remote_name> <URL> --protocol=simplestreams

The URL must use HTTPS.

### Add a remote Incus server

<!-- Include start add remotes -->
To add a Incus server as a remote, enter the following command:

    incus remote add <remote_name> <IP|FQDN|URL> [flags]

Some authentication methods require specific flags (for example, use [`incus remote add <remote_name> <IP|FQDN|URL> --auth-type=oidc`](incus_remote_add.md) for OIDC authentication).
See {ref}`server-authenticate` and {ref}`authentication` for more information.

For example, enter the following command to add a remote through an IP address:

    incus remote add my-remote 192.0.2.10

You are prompted to confirm the remote server fingerprint and then asked for the password or token, depending on the authentication method used by the remote.
<!-- Include end add remotes -->

## Reference an image

To reference an image, specify its remote and its alias or fingerprint, separated with a colon.
For example:

    images:ubuntu/22.04
    images:ubuntu/22.04
    local:ed7509d7e83f

(images-remote-default)=
## Select a default remote

If you specify an image name without the name of the remote, the default image server is used.

To see which server is configured as the default image server, enter the following command:

    incus remote get-default

To select a different remote as the default image server, enter the following command:

    incus remote switch <remote_name>
