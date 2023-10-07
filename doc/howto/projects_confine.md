(projects-confine)=
# How to confine projects to specific users

You can use projects to confine the activities of different users or clients.
See {ref}`projects-confined` for more information.

How to confine a project to a specific user depends on the authentication method you choose.

## Confine projects to specific TLS clients

You can confine access to specific projects by restricting the TLS client certificate that is used to connect to the Incus server.
See {ref}`authentication-tls-certs` for detailed information.

To confine the access from the time the client certificate is added, you must either use token authentication or add the client certificate to the server directly.
If you use password authentication, you can restrict the client certificate only after it has been added.

Use the following command to add a restricted client certificate:

````{tabs}

```{group-tab} Token authentication

    incus config trust add --projects <project_name> --restricted

```

```{group-tab} Add client certificate

    incus config trust add <certificate_file> --projects <project_name> --restricted
```

````

The client can then add the server as a remote in the usual way ([`incus remote add <server_name> <token>`](incus_remote_add.md) or [`incus remote add <server_name> <server_address>`](incus_remote_add.md)) and can only access the project or projects that have been specified.

To confine access for an existing certificate (either because the access restrictions change or because the certificate was added with a trust password), use the following command:

    incus config trust edit <fingerprint>

Make sure that `restricted` is set to `true` and specify the projects that the certificate should give access to under `projects`.

```{note}
You can specify the `--project` flag when adding a remote.
This configuration pre-selects the specified project.
However, it does not confine the client to this project.
```

## Confine projects to specific Incus users

Incus can be configured to dynamically create projects for all users in a specific user group.
This is usually achieved by having some users be a member of the `incus` group but not the `incus-admin` group.

Make sure that all user accounts that you want to be able to use Incus are a member of this group.

Once a member of the group issues a Incus command, Incus creates a confined project for this user and switches to this project.
If Incus has not been {ref}`initialized <initialize>` at this point, it is automatically initialized (with the default settings).

If you want to customize the project settings, for example, to impose limits or restrictions, you can do so after the project has been created.
To modify the project configuration, you must have full access to Incus, which means you must be part of the `incus-admin` group and not only the group that you configured as the Incus user group.
