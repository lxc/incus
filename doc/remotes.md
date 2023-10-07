# How to add remote servers

Remote servers are a concept in the Incus command-line client.
By default, the command-line client interacts with the local Incus daemon, but you can add other servers or clusters to interact with.

One use case for remote servers is to distribute images that can be used to create instances on local servers.
See {ref}`remote-image-servers` for more information.

You can also add a full Incus server as a remote server to your client.
In this case, you can interact with the remote server in the same way as with your local daemon.
For example, you can manage instances or update the server configuration on the remote server.

## Authentication

To be able to add a Incus server as a remote server, the server's API must be exposed, which means that its {config:option}`server-core:core.https_address` server configuration option must be set.

When adding the server, you must then authenticate with it using the chosen method for {ref}`authentication`.

See {ref}`server-expose` for more information.

## List configured remotes

% Include parts of the content from file [howto/images_remote.md](howto/images_remote.md)
```{include} howto/images_remote.md
   :start-after: <!-- Include start list remotes -->
   :end-before: <!-- Include end list remotes -->
```

## Add a remote Incus server

% Include parts of the content from file [howto/images_remote.md](howto/images_remote.md)
```{include} howto/images_remote.md
   :start-after: <!-- Include start add remotes -->
   :end-before: <!-- Include end add remotes -->
```

## Select a default remote

The Incus command-line client is pre-configured with the `local` remote, which is the local Incus daemon.

To select a different remote as the default remote, enter the following command:

    incus remote switch <remote_name>

To see which server is configured as the default remote, enter the following command:

    incus remote get-default

## Configure a global remote

You can configure remotes on a global, per-system basis.
These remotes are available for every user of the Incus server for which you add the configuration.

Users can override these system remotes (for example, by running [`incus remote rename`](incus_remote_rename.md) or [`incus remote set-url`](incus_remote_set-url.md)), which results in the remote and its associated certificates being copied to the user configuration.

To configure a global remote, create or edit a `config.yml` file that is located in `/etc/incus/`.

Certificates for the remotes must be stored in the `servercerts` directory in the same location (for example, `/etc/incus/servercerts/`).
They must match the remote name (for example, `foo.crt`).

See the following example configuration:

```
remotes:
  foo:
    addr: https://192.0.2.4:8443
    auth_type: tls
    project: default
    protocol: incus
    public: false
  bar:
    addr: https://192.0.2.5:8443
    auth_type: tls
    project: default
    protocol: incus
    public: false
```
