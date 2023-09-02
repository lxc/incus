(server)=
# Server configuration

The LXD server can be configured through a set of key/value configuration options.

The key/value configuration is namespaced.
The following options are available:

- {ref}`server-options-core`
- {ref}`server-options-acme`
- {ref}`server-options-cluster`
- {ref}`server-options-images`
- {ref}`server-options-loki`
- {ref}`server-options-misc`

See {ref}`server-configure` for instructions on how to set the configuration options.

```{note}
Options marked with a `global` scope in the following tables are immediately applied to all cluster members.
Options with a `local` scope must be set on a per-member basis.
```

(server-options-core)=
## Core configuration

The following server options control the core daemon configuration:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`core.bgp_address`                  | string    | local     | -                                                | Address to bind the BGP server to (BGP)
`core.bgp_asn`                      | string    | global    | -                                                | The BGP Autonomous System Number to use for the local server
`core.bgp_routerid`                 | string    | local     | -                                                | A unique identifier for this BGP server (formatted as an IPv4 address)
`core.debug_address`                | string    | local     | -                                                | Address to bind the `pprof` debug server to (HTTP)
`core.dns_address`                  | string    | local     | -                                                | Address to bind the authoritative DNS server to (DNS)
`core.https_address`                | string    | local     | -                                                | Address to bind for the remote API (HTTPS)
`core.https_allowed_credentials`    | bool      | global    | -                                                | Whether to set the `Access-Control-Allow-Credentials` HTTP header value to `true`
`core.https_allowed_headers`        | string    | global    | -                                                | `Access-Control-Allow-Headers` HTTP header value
`core.https_allowed_methods`        | string    | global    | -                                                | `Access-Control-Allow-Methods` HTTP header value
`core.https_allowed_origin`         | string    | global    | -                                                | `Access-Control-Allow-Origin` HTTP header value
`core.https_trusted_proxy`          | string    | global    | -                                                | Comma-separated list of IP addresses of trusted servers to provide the client's address through the proxy connection header
`core.metrics_address`              | string    | global    | -                                                | Address to bind the metrics server to (HTTPS)
`core.metrics_authentication`       | bool      | global    | `true`                                           | Whether to enforce authentication on the metrics endpoint
`core.proxy_https`                  | string    | global    | -                                                | HTTPS proxy to use, if any (falls back to `HTTPS_PROXY` environment variable)
`core.proxy_http`                   | string    | global    | -                                                | HTTP proxy to use, if any (falls back to `HTTP_PROXY` environment variable)
`core.proxy_ignore_hosts`           | string    | global    | -                                                | Hosts that don't need the proxy (similar format to `NO_PROXY`, for example, `1.2.3.4,1.2.3.5`, falls back to `NO_PROXY` environment variable)
`core.remote_token_expiry`          | string    | global    | -                                                | Time after which a remote add token expires (defaults to no expiry)
`core.shutdown_timeout`             | integer   | global    | `5`                                              | Number of minutes to wait for running operations to complete before the LXD server shuts down
`core.storage_buckets_address`      | string    | local     | -                                                | Address to bind the storage object server to (HTTPS)
`core.trust_ca_certificates`        | bool      | global    | -                                                | Whether to automatically trust clients signed by the CA

(server-options-acme)=
## ACME configuration

The following server options control the {ref}`ACME <authentication-server-certificate>` configuration:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`acme.agree_tos`                    | bool      | global    | `false`                                          | Agree to ACME terms of service
`acme.ca_url`                       | string    | global    | `https://acme-v02.api.letsencrypt.org/directory` | URL to the directory resource of the ACME service
`acme.domain`                       | string    | global    | -                                                | Domain for which the certificate is issued
`acme.email`                        | string    | global    | -                                                | Email address used for the account registration

(server-options-oidc)=
## OpenID Connect configuration
Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`oidc.client.id`                    | string    | global    | -                                                | OpenID Connect client ID
`oidc.issuer`                       | string    | global    | -                                                | OpenID Connect Discovery URL for the provider
`oidc.audience`                     | string    | global    | -                                                | Expected audience value for the application (required by some providers)

(server-options-cluster)=
## Cluster configuration

The following server options control {ref}`clustering`:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`cluster.healing_threshold`         | integer   | global    | `0`                                              | Number of seconds after which an offline cluster member is to be evacuated (set to `0` to disable)
`cluster.https_address`             | string    | local     | -                                                | Address to use for clustering traffic
`cluster.images_minimal_replica`    | integer   | global    | `3`                                              | Minimal number of cluster members with a copy of a particular image (set to `1` for no replication or to `-1` for all members)
`cluster.join_token_expiry`         | string    | global    | `3H`                                             | Time after which a cluster join token expires
`cluster.max_standby`               | integer   | global    | `2`                                              | Maximum number of cluster members that are assigned the database stand-by role (must be between `0` and `5`)
`cluster.max_voters`                | integer   | global    | `3`                                              | Maximum number of cluster members that are assigned the database voter role (must be an odd number >= `3`)
`cluster.offline_threshold`         | integer   | global    | `20`                                             | Number of seconds after which an unresponsive member is considered offline

(server-options-images)=
## Images configuration

The following server options configure how to handle {ref}`images`:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`images.auto_update_cached`         | bool      | global    | `true`                                           | Whether to automatically update any image that LXD caches
`images.auto_update_interval`       | integer   | global    | `6`                                              | Interval (in hours) at which to look for updates to cached images (`0` to disable)
`images.compression_algorithm`      | string    | global    | `gzip`                                           | Compression algorithm to use for new images (`bzip2`, `gzip`, `lzma`, `xz` or `none`)
`images.default_architecture`       | string    | -         | -                                                | Default architecture to use in a mixed-architecture cluster
`images.remote_cache_expiry`        | integer   | global    | `10`                                             | Number of days after which an unused cached remote image is flushed

(server-options-loki)=
## Loki configuration

The following server options configure the external log aggregation system:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`loki.api.ca_cert`                  | string    | global    | -                                                | The CA certificate for the Loki server
`loki.api.url`                      | string    | global    | -                                                | The URL to the Loki server
`loki.auth.password`                | string    | global    | -                                                | The password used for authentication
`loki.auth.username`                | string    | global    | -                                                | The user name used for authentication
`loki.labels`                       | string    | global    | -                                                | Comma-separated list of values that should be used as labels for a Loki log entry
`loki.loglevel`                     | string    | global    | `info`                                           | Minimum log level to send to the Loki server
`loki.types`                        | string    | global    | `lifecycle,logging`                              | Comma-separated list of events to send to the Loki server (`lifecycle` and/or `logging`)

(server-options-misc)=
## Miscellaneous options

The following server options configure server-specific settings for {ref}`instances`, {ref}`OVN <network-ovn>` integration, {ref}`Backups <backups>` and {ref}`storage`:

```{rst-class} break-col-4 min-width-4-8
```

(server-options)=
Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`backups.compression_algorithm`     | string    | global    | `gzip`                                           | Compression algorithm to use for backups (`bzip2`, `gzip`, `lzma`, `xz` or `none`)
`instances.nic.host_name`           | string    | global    | `random`                                         | If set to `random`, use the random host interface name as the host name; if set to `mac`, generate a host name in the form `lxd<mac_address>` (MAC without leading two digits)
`instances.placement.scriptlet`     | string    | global    | -                                                | Stores the {ref}`clustering-instance-placement-scriptlet` for custom automatic instance placement logic
`network.ovn.integration_bridge`    | string    | global    | `br-int`                                         | OVS integration bridge to use for OVN networks
`network.ovn.northbound_connection` | string    | global    | `unix:/var/run/ovn/ovnnb_db.sock`                | OVN northbound database connection string
`storage.backups_volume`            | string    | local     | -                                                | Volume to use to store the backup tarballs (syntax is `POOL/VOLUME`)
`storage.images_volume`             | string    | local     | -                                                | Volume to use to store the image tarballs (syntax is `POOL/VOLUME`)
