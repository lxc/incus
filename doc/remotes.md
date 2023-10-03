# How to add remote servers

Remote servers are a concept in the LXD command-line client.
By default, the command-line client interacts with the local LXD daemon, but you can add other servers or clusters to interact with.

One use case for remote servers is to distribute images that can be used to create instances on local servers.
See {ref}`remote-image-servers` for more information.

You can also add a full LXD server as a remote server to your client.
In this case, you can interact with the remote server in the same way as with your local daemon.
For example, you can manage instances or update the server configuration on the remote server.

## Authentication

To be able to add a LXD server as a remote server, the server's API must be exposed, which means that its {config:option}`server-core:core.https_address` server configuration option must be set.

When adding the server, you must then authenticate with it using the chosen method for {ref}`authentication`.

See {ref}`server-expose` for more information.

## List configured remotes

% Include parts of the content from file [howto/images_remote.md](howto/images_remote.md)
```{include} howto/images_remote.md
   :start-after: <!-- Include start list remotes -->
   :end-before: <!-- Include end list remotes -->
```

## Add a remote LXD server

% Include parts of the content from file [howto/images_remote.md](howto/images_remote.md)
```{include} howto/images_remote.md
   :start-after: <!-- Include start add remotes -->
   :end-before: <!-- Include end add remotes -->
```

## Select a default remote

The LXD command-line client is pre-configured with the `local` remote, which is the local LXD daemon.

To select a different remote as the default remote, enter the following command:

    lxc remote switch <remote_name>

To see which server is configured as the default remote, enter the following command:

    lxc remote get-default

## Configure a global remote

You can configure remotes on a global, per-system basis.
These remotes are available for every user of the LXD server for which you add the configuration.

Users can override these system remotes (for example, by running `lxc remote rename` or `lxc remote set-url`), which results in the remote and its associated certificates being copied to the user configuration.

To configure a global remote, edit the `config.yml` file that is located in one of the following directories:

- the directory specified by `LXD_GLOBAL_CONF` (if defined)
- `/var/snap/lxd/common/global-conf/` (if you use the snap)
- `/etc/lxd/` (otherwise)

Certificates for the remotes must be stored in the `servercerts` directory in the same location (for example, `/etc/lxd/servercerts/`).
They must match the remote name (for example, `foo.crt`).

See the following example configuration:

```
remotes:
  foo:
    addr: https://192.0.2.4:8443
    auth_type: tls
    project: default
    protocol: lxd
    public: false
  bar:
    addr: https://192.0.2.5:8443
    auth_type: tls
    project: default
    protocol: lxd
    public: false
```
