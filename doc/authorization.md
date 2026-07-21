(authorization)=
# Authorization

When interacting with Incus over the Unix socket, members of the `incus-admin` group will have full access to the Incus API.
Those who are only members of the `incus` group will instead be restricted to a single project tied to their user.

When interacting with Incus over the network (see {ref}`server-expose` for instructions), it is possible to further authenticate and restrict user access.
There are three supported authorization methods:

- {ref}`authorization-tls`
- {ref}`authorization-openfga`
- {ref}`authorization-scriptlet`

By default, the method used for a request is determined automatically from the
client's authentication protocol. This can be customized so that different kinds
of clients are handled by different methods; see {ref}`authorization-client-routing`.

(authorization-tls)=
## TLS authorization

Incus natively supports restricting {ref}`authentication-trusted-clients` to one or more projects.
When a client certificate is restricted, the client will also be prevented from performing global configuration changes or altering the configuration (limits, restrictions) of the projects it's allowed access to.

To restrict access, use [`incus config trust edit <fingerprint>`](incus_config_trust_edit.md).
Set the `restricted` key to `true` and specify a list of projects to restrict the client to.
If the list of projects is empty, the client will not be allowed access to any of them.

By default, this authorization method is used for clients authenticating with a restricted TLS certificate.
Clients using an unrestricted certificate are granted full access instead.
Both can be changed, see {ref}`authorization-client-routing`.

(authorization-openfga)=
## Open Fine-Grained Authorization (OpenFGA)

Incus supports integrating with [{abbr}`OpenFGA (Open Fine-Grained Authorization)`](https://openfga.dev).
This authorization method is highly granular.
For example, it can be used to restrict user access to a single instance.

To use OpenFGA for authorization, you must configure and run an OpenFGA server yourself.
Incus will connect to the OpenFGA server, write the {ref}`openfga-model`, and query this server for authorization for all subsequent requests.

To enable this authorization method in Incus, set the [`authorization.openfga.*`](server-options-authorization) server configuration options.
All options must be set in order to enable OpenFGA. Though, you do not have to create the authorization-model yourself, Incus will generate it including the initial tuple to allow only authenticated users: `server:incus#authenticated@user:*`.

```{warning}
Setting the `authorization.openfga.*` options only makes OpenFGA available.
It does not on its own cause any request to be authorized by it.

OpenFGA is only consulted for clients whose class is routed to it with an
`authorization.client.*` option, see {ref}`authorization-client-routing`.
```

(openfga-model)=
### OpenFGA model

With OpenFGA, access to a particular API resource is determined by the user's relationship to it.
These relationships are determined by an [OpenFGA authorization model](https://openfga.dev/docs/concepts#what-is-an-authorization-model).
The Incus OpenFGA authorization model describes API resources in terms of their relationship to other resources, and a relationship a user or group might have with that resource.

The full Incus OpenFGA authorization model is defined in `internal/server/auth/driver_openfga_model.openfga`:

```{literalinclude} ../internal/server/auth/driver_openfga_model.openfga
:language: none
```

```{important}
Users that you do not trust with root access to the host should not be granted the following relations:

- `server -> admin`
- `server -> operator`
- `server -> can_edit`
- `server -> can_create_storage_pools`
- `server -> can_create_projects`
- `server -> can_create_certificates`
- `certificate -> can_edit`
- `storage_pool -> can_edit`
- `project -> admin`

The remaining relations may be granted.
However, you must apply appropriate {ref}`project-restrictions`.
```

(authorization-scriptlet)=
## Scriptlet authorization

Incus supports defining a scriptlet to manage fine-grained authorization, allowing to write precise authorization rules with no dependency on external tools.

```{warning}
Setting `authorization.scriptlet` only makes the scriptlet available.
It does not on its own cause any request to be authorized by it.

The scriptlet is only run for clients whose class is routed to it with an
`authorization.client.*` option, see {ref}`authorization-client-routing`.
```

To use scriptlet authorization, you can write a scriptlet in the `authorization.scriptlet` server configuration option implementing a function `authorize`, which takes three arguments:

- `details`, an object with the following attributes:
   - `Username`: the user name or certificate fingerprint
   - `Protocol`: the authentication protocol
   - `IsAllProjectsRequest`: whether the request is made on all projects
   - `ProjectName`: the project name
   - `Chain`: the certificate chain as a list of dissected x509 certificates
   - `Certificate`: the certificate data stored in the database
- `object`, the object on which the user requests authorization
- `entitlement`, the authorization level asked by the user

This function must return a Boolean indicating whether the user has access or not to the given object with the given entitlement.

Additionally, two optional functions can be defined so that users can be listed through the access API:

- `get_instance_access`, with two arguments (`project_name` and `instance_name`), returning a list of users able to access a given instance
- `get_project_access`, with one argument (`project_name`), returning a list of users able to access a given project

(authorization-client-routing)=
## Client routing

More than one authorization method can be loaded at the same time, with each
request routed to exactly one of them based on the authentication class of the
client. This is controlled through the `authorization.client.*` server
configuration options, one per client class:

- `authorization.client.unix`: local clients connecting over the Unix socket
- `authorization.client.tls`: clients using an unrestricted client certificate
- `authorization.client.tls-restricted`: clients using a restricted (project-scoped) client certificate
- `authorization.client.oidc`: OIDC-authenticated clients
- `authorization.client.default`: any client class not set above

Each option accepts one of the following values:

- `allow`: unconditionally grant access
- `deny`: unconditionally refuse access
- `tls`: use {ref}`authorization-tls`, only valid for `authorization.client.tls-restricted`
- `openfga`: use {ref}`authorization-openfga`
- `scriptlet`: use {ref}`authorization-scriptlet`

A per-class option falls back to `authorization.client.default` when unset.
When that is unset too, the following built-in routing applies:

| Client class     | Built-in route |
|------------------|----------------|
| `unix`           | `allow`        |
| `tls`            | `allow`        |
| `tls-restricted` | `tls`          |
| `oidc`           | `allow`        |
| `default`        | `deny`         |

This routing is fixed.

```{warning}
The per-certificate project restrictions described in {ref}`authorization-tls`
are enforced by the TLS authorization method only.

If `authorization.client.tls-restricted` is set to any value other than `tls`,
the project list of the client certificate are no longer consulted, and a restricted
certificate may gain access well beyond the projects it is limited to.
Only set this option to something other than `tls` if the method you route to
reproduces the restrictions you rely on.
```

```{warning}
Always set `authorization.client.default` last, once you have confirmed that
every explicitly configured client class behaves as expected.

The default applies to every class that is not set explicitly, so a single
mistake affects all of them at once. If it is set to `openfga` or `scriptlet`
while that method is misconfigured or unreachable, or to `deny`, every remote
client is refused.

The `root` user is always allowed to interact with the API over the Unix
socket, regardless of the configured authorization method. This is what allows
a server locked out by a bad routing configuration to be recovered.
```

For example, to have OpenFGA authorize OIDC clients while restricted TLS clients
keep using TLS authorization:

    incus config set authorization.client.oidc=openfga
    incus config set authorization.client.tls-restricted=scriptlet
