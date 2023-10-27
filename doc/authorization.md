(authorization)=
# Authorization

When interacting with Incus over the Unix socket, clients have full access to Incus API.

However, it is possible to restrict user access to the Incus API when communicating via remote HTTPS (see {ref}`server-expose` for instructions).
There are three supported authorization methods:

- {ref}`authorization-tls`
- {ref}`authorization-openfga`

(authorization-tls)=
## TLS authorization

Incus natively supports restricting {ref}`authentication-trusted-clients` to one or more projects.
When a client certificate is restricted, the client will also be prevented from performing global configuration changes or altering the configuration (limits, restrictions) of the projects it's allowed access to.

To restrict access, use [`incus config trust edit <fingerprint>`](incus_config_trust_edit.md).
Set the `restricted` key to `true` and specify a list of projects to restrict the client to.
If the list of projects is empty, the client will not be allowed access to any of them.

This authorization method is always used if a client authenticates with TLS, regardless of whether another authorization method is configured.

(authorization-openfga)=
## Open Fine-Grained Authorization (OpenFGA)

Incus supports integrating with [{abbr}`OpenFGA (Open Fine-Grained Authorization)`](https://openfga.dev).
This authorization method is highly granular.
For example, it can be used to restrict user access to a single instance.

To use OpenFGA for authorization, you must configure and run an OpenFGA server yourself.
To enable this authorization method in Incus, set the [`openfga.*`](server-options-openfga) server configuration options.
Incus will connect to the OpenFGA server, write the {ref}`openfga-model`, and query this server for authorization for all subsequent requests.

(openfga-model)=
### OpenFGA model

With OpenFGA, access to a particular API resource is determined by the users relationship to it.
These relationships are determined by an [OpenFGA authorization model](https://openfga.dev/docs/concepts#what-is-an-authorization-model).
The Incus OpenFGA authorization model describes API resources in terms of their relationship to other resources, and a relationship a user or group may have with that resource.
Some convenient relations have also been built into the model:

- `server -> admin`: Full access to Incus.
- `server -> operator`: Full access to Incus, without edit access on server configuration, certificates, or storage pools.
- `server -> viewer`: Can view all server level configuration but cannot edit. Cannot view projects or their contents.
- `project -> manager`: Full access to a single project, including edit access.
- `project -> operator`: Full access to a single project, without edit access.
- `project -> viewer`: View access for a single project.
- `instance -> manager`: Full access to a single instance, including edit access.
- `instance -> operator`: Full access to a single  instance, without edit access.
- `instance -> user`: View access to a single instance, plus permissions for `exec`, `console`, and `file` APIs.
- `instance -> viewer`: View access to a single instance.

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
- `project -> manager`

Remaining relations may be granted, however you must apply appropriate {ref}`project-restrictions`.
```

The full Incus OpenFGA authorization model is shown below.

```{literalinclude} ../internal/server/auth/driver_openfga_model.openfga
---
language: none
---
```
