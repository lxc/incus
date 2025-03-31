(authentication)=
# Remote API authentication

Remote communications with the Incus daemon happen using JSON over HTTPS.

To be able to access the remote API, clients must authenticate with the Incus server.
The following authentication methods are supported:

- {ref}`authentication-tls-certs`
- {ref}`authentication-openid`

(authentication-tls-certs)=
## TLS client certificates

When using {abbr}`TLS (Transport Layer Security)` client certificates for authentication, both the client and the server will generate a key pair the first time they're launched.
The server will use that key pair for all HTTPS connections to the Incus socket.
The client will use its certificate as a client certificate for any client-server communication.

To cause certificates to be regenerated, simply remove the old ones.
On the next connection, a new certificate is generated.

### Communication protocol

The supported protocol must be TLS 1.3 or better.

It's possible to force Incus to accept TLS 1.2 by setting the `INCUS_INSECURE_TLS` environment variable on both client and server.
However this isn't a supported setup and should only ever be used when forced to use an outdated corporate proxy.

All communications must use perfect forward secrecy, and ciphers must be limited to strong elliptic curve ones (such as ECDHE-RSA or ECDHE-ECDSA).

Any generated key should be at least 4096 bit RSA, preferably 384 bit ECDSA.
When using signatures, only SHA-2 signatures should be trusted.

Since we control both client and server, there is no reason to support
any backward compatibility to broken protocol or ciphers.

(authentication-trusted-clients)=
### Trusted TLS clients

You can obtain the list of TLS certificates trusted by an Incus server with [`incus config trust list`](incus_config_trust_list.md).

Trusted clients can be added in either of the following ways:

- {ref}`authentication-add-certs`
- {ref}`authentication-token`

The workflow to authenticate with the server is similar to that of SSH, where an initial connection to an unknown server triggers a prompt:

1. When the user adds a server with [`incus remote add`](incus_remote_add.md), the server is contacted over HTTPS, its certificate is downloaded and the fingerprint is shown to the user.
1. The user is asked to confirm that this is indeed the server's fingerprint, which they can manually check by connecting to the server or by asking someone with access to the server to run the info command and compare the fingerprints.
1. The server attempts to authenticate the client:

   - If the client certificate is in the server's trust store, the connection is granted.
   - If the client certificate is not in the server's trust store, the server prompts the user for a token.
     If the provided token matches, the client certificate is added to the server's trust store and the connection is granted.
     Otherwise, the connection is rejected.

It is possible to restrict a TLS client's access to Incus via {ref}`authorization-tls`.
To revoke trust to a client, remove its certificate from the server with [`incus config trust remove <fingerprint>`](incus_config_trust_remove.md).

(authentication-tls-jwt)=
#### Using `JSON Web Token` (`JWT`) to perform TLS authentication

As an alternative to directly using the client's TLS certificate for
authentication, Incus also supports the user derive a `bearer` token and
use it through the HTTP `Authorization` header.

To do this, the user must generate a signed `JWT` which has its
`Subject` field set to the full fingerprint of their client certificate,
it must have valid `NotBefore` and `NotAfter` fields and be signed by
the client certificate's private key.

(authentication-add-certs)=
#### Adding trusted certificates to the server

The preferred way to add trusted clients is to directly add their certificates to the trust store on the server.
To do so, copy the client certificate to the server and register it using [`incus config trust add-certificate <file>`](incus_config_trust_add-certificate.md).

(authentication-token)=
#### Adding client certificates using tokens

You can also add new clients by using tokens. Tokens expire after a configurable time ({config:option}`server-core:core.remote_token_expiry`) or once they've been used.

To use this method, generate a token for each client by calling [`incus config trust add`](incus_config_trust_add.md), which will prompt for the client name.
The clients can then add their certificates to the server's trust store by providing the generated token when prompted.

<!-- Include start NAT authentication -->

```{note}
If your Incus server is behind NAT, you must specify its external public address when adding it as a remote for a client:

    incus remote add <name> <IP_address>

When generating the token on the server, Incus includes a list of IP addresses that the client can use to access the server.
However, if the server is behind NAT, these addresses might be local addresses that the client cannot connect to.
In this case, you must specify the external address manually.
```

<!-- Include end NAT authentication -->

Alternatively, the clients can provide the token directly when adding the remote: [`incus remote add <name> <token>`](incus_remote_add.md).

### Using a PKI system

In a {abbr}`PKI (Public key infrastructure)` setup, a system administrator manages a central PKI that issues client certificates for all the Incus clients and server certificates for all the Incus daemons.

To enable PKI mode, complete the following steps:

1. Add the {abbr}`CA (Certificate authority)` certificate to all machines:

   - Place the `client.ca` file in the clients' configuration directories (`~/.config/incus`).
   - Place the `server.ca` file in the server's configuration directory (`/var/lib/incus`).
1. Place the certificates issued by the CA on the clients and the server, replacing the automatically generated ones.
1. Restart the server.

In that mode, any connection to an Incus daemon will be done using the
pre-seeded CA certificate.

If the server certificate isn't signed by the CA, the connection will simply go through the normal authentication mechanism.
If the server certificate is valid and signed by the CA, then the connection continues without prompting the user for the certificate.

Note that the generated certificates are not automatically trusted. You must still add them to the server in one of the ways described in {ref}`authentication-trusted-clients`.

### Encrypting local keys

The `incus` client also supports encrypted client keys. Keys generated via the methods above can be encrypted with a password, using:

```
ssh-keygen -p -o -f .config/incus/client.key
```

```{note}
Unless you enable [`keepalive` mode](remote-keepalive), then every single call to Incus will cause the prompt which may get a bit annoying:

    $ incus list remote-host:
    Password for client.key:
    +------+-------+------+------+------+-----------+
    | NAME | STATE | IPV4 | IPV6 | TYPE | SNAPSHOTS |
    +------+-------+------+------+------+-----------+
```

```{note}
While the `incus` command line supports encrypted keys, tools such as [Ansible's connection plugin](https://docs.ansible.com/ansible/latest/collections/community/general/incus_connection.html) do not.
```

(authentication-openid)=
## OpenID Connect authentication

Incus supports using [OpenID Connect](https://openid.net/connect/) to authenticate users through an {abbr}`OIDC (OpenID Connect)` Identity Provider.

```{note}
Authentication through OpenID Connect is supported, but there is no user role handling in place so far.
Any user that authenticates through the configured OIDC Identity Provider gets full access to Incus.
```

To configure Incus to use OIDC authentication, set the [`oidc.*`](server-options-oidc) server configuration options.
Your OIDC provider must be configured to enable the [Device Authorization Grant](https://oauth.net/2/device-flow/) type.

To add a remote pointing to an Incus server configured with OIDC authentication, run [`incus remote add <remote_name> <remote_address>`](incus_remote_add.md).
You are then prompted to authenticate through your web browser, where you must confirm the device code that Incus uses.
The Incus client then retrieves and stores the access and refresh tokens and provides those to Incus for all interactions.

```{important}
Any user that authenticates through the configured OIDC Identity Provider gets full access to Incus.
To restrict user access, you must also configure {ref}`authorization`.
Currently, the only authorization method that is compatible with OIDC is {ref}`authorization-openfga`.
```

(authentication-server-certificate)=
## TLS server certificate

Incus supports issuing server certificates using {abbr}`ACME (Automatic Certificate Management Environment)` services, for example, [Let's Encrypt](https://letsencrypt.org/).

To enable this feature, set the [relevant server configuration options](server-options-acme).

Incus supports both `HTTP-01` and `DNS-01` challenges. The set of configuration option varies between the two.

For `DNS-01`, the relevant {config:option}`server-acme:acme.provider` and {config:option}`server-acme:acme.provider.environment`
values can be found directly in the [documentation of `lego`](https://go-acme.github.io/lego/dns/index.html),
the ACME client that Incus uses behind the scenes.

For `HTTP-01`, Incus will cause `lego` to temporarily listen on port `80` so the the HTTP challenge can go through.
If your Incus server sits behind a reverse proxy, you'll need that reverse proxy to redirect HTTP traffic to HTTPS.

## Failure scenarios

In the following scenarios, authentication is expected to fail.

### Server certificate changed

The server certificate might change in the following cases:

- The server was fully reinstalled and therefore got a new certificate.
- The connection is being intercepted ({abbr}`MITM (Machine in the middle)`).

In such cases, the client will refuse to connect to the server because the certificate fingerprint does not match the fingerprint in the configuration for this remote.

It is then up to the user to contact the server administrator to check if the certificate did in fact change.
If it did, the certificate can be replaced by the new one, or the remote can be removed altogether and re-added.

### Server trust relationship revoked

The server trust relationship is revoked for a client if another trusted client or the local server administrator removes the trust entry for the client on the server.

In this case, the server still uses the same certificate, but all API calls return a 403 code with an error indicating that the client isn't trusted.
