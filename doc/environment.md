# Environment variables

The Incus client and daemon respect some environment variables to adapt to
the user's environment and to turn some advanced features on and off.

## Common

Name                            | Description
:---                            | :----
`INCUS_DIR`                     | The Incus data directory
`INCUS_INSECURE_TLS`            | If set to true, allows all default Go ciphers both for client <-> server communication and server <-> image servers (server <-> server and clustering are not affected)
`PATH`                          | List of paths to look into when resolving binaries
`http_proxy`                    | Proxy server URL for HTTP
`https_proxy`                   | Proxy server URL for HTTPS
`no_proxy`                      | List of domains, IP addresses or CIDR ranges that don't require the use of a proxy

## Client environment variable

Name                            | Description
:---                            | :----
`EDITOR`                        | What text editor to use
`VISUAL`                        | What text editor to use (if `EDITOR` isn't set)
`INCUS_CONF`                    | Path to the client configuration directory
`INCUS_GLOBAL_CONF`             | Path to the global client configuration directory
`INCUS_REMOTE`                  | Name of the remote to use (overrides configured default remote)
`INCUS_PROJECT`                 | Name of the project to use (overrides configured default project)

## Server environment variable

Name                            | Description
:---                            | :----
`INCUS_AGENT_PATH`              | Path to the directory including the `incus-agent` builds
`INCUS_CLUSTER_UPDATE`          | Script to call on a cluster update
`INCUS_DEVMONITOR_DIR`          | Path to be monitored by the device monitor. This is primarily for testing
`INCUS_DOCUMENTATION`           | Path to the documentation to serve through the web server
`INCUS_EXEC_PATH`               | Full path to the Incus binary (used when forking subcommands)
`INCUS_IDMAPPED_MOUNTS_DISABLE` | Disable idmapped mounts support (useful when testing traditional UID shifting)
`INCUS_LXC_TEMPLATE_CONFIG`     | Path to the LXC template configuration directory
`INCUS_EDK2_PATH`               | Path to EDK2 firmware build including `*_CODE.fd` and `*_VARS.fd`
`INCUS_SECURITY_APPARMOR`       | If set to `false`, forces AppArmor off
`INCUS_UI`                      | Path to the web UI to serve through the web server
`INCUS_USBIDS_PATH`             | Path to the hwdata `usb.ids` file
