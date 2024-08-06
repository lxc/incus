(cluster-access)=
# Accessing a cluster
An Incus cluster generally behaves much like a standalone Incus server.

A client can talk to any of the servers within a cluster and will get an
identical experience. Requests can be directed at a specific server
through the API and doesn't need a direct connection to that server.

```{note}
Targeting a specific server is done through `?target=SERVER` at the API level or `--target` at the CLI level).
```

The cluster uses a single client facing TLS certificate for all servers,
this makes it easier to expose a valid HTTPS endpoint to clients,
avoiding having to manually check fingerprints.

You can use `incus cluster update-certificate` to load your own
cluster-wide TLS certificate, or you can use ACME to automatically issue
and deploy a certificate across the cluster (see {ref}`authentication-server-certificate`).

## Authentication
### HTTPS with TLS
The default authentication method when dealing with a remote Incus cluster.

This works fine in a cluster, but may cause some issues with some
proxies and load-balancers that want to establish their own TLS
connection to the cluster.

See {ref}`authentication-tls-certs` for details.

### HTTPS with OpenID Connect (OIDC)
OpenID Connect authentication on Incus requires an external OpenID
Identity Provider but then has the advantage of offering fine grained
authentication to a cluster, making managing and auditing access easy.

For OpenID Connect to work properly, the cluster will need to have a DNS
record, a valid certificate and be able to reach the OpenID Identity
Provider.

See {ref}`authentication-openid` for details.

### Local access
You can also interact with a cluster by connecting to any of the
clustered servers and talking to the local Incus daemon running on that
server.

## High availability
To provide a highly available Incus API on a cluster, you need to have
client requests always make it to at least one responsive server.

Here are a few common ways to handle it.

### DNS round-robin
DNS is a very easy way to balance API traffic over multiple servers.
Simply create a DNS record with an `A` or `AAAA` for each server in the cluster.

While this is trivial to put in place, it will only properly handle falling back to another server if the server quickly rejects the connection.
Any stuck server may cause significant delays for some clients as they'll need to wait for a full connection timeout before another server is contacted.

### External load-balancer
A reasonably easy solution is to run a load-balancer, either a self-hosted one like `haproxy` or one provide by your existing network or cloud infrastructure.

Those load-balancers can often monitor service health and only send requests to servers that are currently responsive.
Incus supports the `haproxy` proxy protocol headers so the original client IP address is reported in log and audit messages.

```{note}
TLS client certificate authentication only works with load-balancers that act at the TCP level.

Load-balancers which terminate the TLS session and then establish their own to Incus can only be used with OIDC authentication.
```

### Floating IP address
It's possible to use Incus with an additional floating IP, effectively a virtual IP address which is only live on one of the servers.
This centralizes all client API traffic to that single server but may be easier to manage in some environments.

For that you'll need to make sure that all servers are configured to listen on all interfaces (e.g. `core.https_address=:8443`)
and then make use of a local firewall to only allow external clients to connect to the virtual IP address.

Common solutions to handle a virtual IP address are `VRRP` (through something like `frr`) and `corosync/pacemaker`.

### ECMP
For those running a full L3 network infrastructure with BGP to each individual host, it's possible to advertise an IP address for use for Incus client traffic.

This IP address would be added to all servers in their network configuration (as a `/32` for IPv4 or `/128` for IPv6) and then advertised to their router.
This will result in the router having an equal cost route for the IP address to all servers in the cluster (ECMP).

Traffic will then get balanced between all servers and as soon as a server goes down, its route will go away and traffic will head to the remaining servers.

```{note}
To minimize fallback delay, one can make use of BFD alongside BGP to get sub-1s fallback time.
```
