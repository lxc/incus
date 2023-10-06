(server-configure)=
# How to configure the Incus server

See {ref}`server` for all configuration options that are available for the Incus server.

If the Incus server is part of a cluster, some of the options apply to the cluster, while others apply only to the local server, thus the cluster member.
In the {ref}`server` option tables, options that apply to the cluster are marked with a `global` scope, while options that apply to the local server are marked with a `local` scope.

## Configure server options

You can configure a server option with the following command:

    incus config set <key> <value>

For example, to allow remote access to the Incus server on port 8443, enter the following command:

    incus config set core.https_address :8443

In a cluster setup, to configure a server option for a cluster member only, add the `--target` flag.
For example, to configure where to store image tarballs on a specific cluster member, enter a command similar to the following:

    incus config set storage.images_volume my-pool/my-volume --target member02

## Display the server configuration

To display the current server configuration, enter the following command:

    incus config show

In a cluster setup, to show the local configuration for a specific cluster member, add the `--target` flag.

## Edit the full server configuration

To edit the full server configuration as a YAML file, enter the following command:

    incus config edit

In a cluster setup, to edit the local configuration for a specific cluster member, add the `--target` flag.
