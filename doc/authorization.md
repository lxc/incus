(authorization)=
# Authorization

When interacting with Incus over the Unix socket, members of the `incus-admin` group will have full access to the Incus API.
Those who are only members of the `incus` group will instead be restricted to a single project tied to their user.

When interacting with Incus over the network (see {ref}`server-expose` for instructions), it is possible to further authenticate and restrict user access.
There are three supported authorization methods:

- {ref}`authorization-tls`
- {ref}`authorization-openfga`
- {ref}`authorization-scriptlet`

(authorization-tls)=
## TLS authorization

Incus natively supports restricting {ref}`authentication-trusted-clients` to one or more projects.
When a client certificate is restricted, the client will also be prevented from performing global configuration changes or altering the configuration (limits, restrictions) of the projects it's allowed access to.

To restrict access, use [`incus config trust edit <fingerprint>`](incus_config_trust_edit.md).
Set the `restricted` key to `true` and specify a list of projects to restrict the client to.
If the list of projects is empty, the client will not be allowed access to any of them.

This authorization method is used if a client authenticates with TLS even if {ref}`OpenFGA authorization <authorization-openfga>` is configured.

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

With OpenFGA, access to a particular API resource is determined by the user's relationship to it.
These relationships are determined by an [OpenFGA authorization model](https://openfga.dev/docs/concepts#what-is-an-authorization-model).
The Incus OpenFGA authorization model describes API resources in terms of their relationship to other resources, and a relationship a user or group might have with that resource.

The full Incus OpenFGA authorization model is defined in `internal/server/auth/driver_openfga_model.openfga`:

```{literalinclude} ../internal/server/auth/driver_openfga_model.openfga
---
language: none
---
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
- `project -> manager`

The remaining relations may be granted.
However, you must apply appropriate {ref}`project-restrictions`.
```

(authorization-scriptlet)=
## Scriptlet authorization

Incus supports defining a scriptlet to manage fine-grained authorization, allowing to write precise authorization rules with no dependency on external tools.

To use scriptlet authorization, you can write a scriptlet in the `authorization.scriptlet` server configuration option implementing a function `authorize`, which takes three arguments:

- `details`, an object with attributes `Username` (the user name or certificate fingerprint), `Protocol` (the authentication protocol), `IsAllProjectsRequest` (whether the request is made on all projects) and `ProjectName` (the project name)
- `object`, the object on which the user requests authorization
- `entitlement`, the authorization level asked by the user

This function must return a Boolean indicating whether the user has access or not to the given object with the given entitlement.

Additionally, two optional functions can be defined so that users can be listed through the access API:

- `get_instance_access`, with two arguments (`project_name` and `instance_name`), returning a list of users able to access a given instance
- `get_project_access`, with one argument (`project_name`), returning a list of users able to access a given project
