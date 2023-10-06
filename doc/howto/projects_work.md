(projects-work)=
# How to work with different projects

If you have more projects than just the `default` profile, you must make sure to use or address the correct project when working with Incus.

```{note}
If you have projects that are {ref}`confined to specific users <projects-confined>`, only users with full access to Incus can see all projects.

Users without full access can only see information for the projects to which they have access.
```

## List projects

To list all projects (that you have permission to see), enter the following command:

    incus project list

By default, the output is presented as a list:

```{terminal}
:input: incus project list
:scroll:

+----------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
|      NAME            | IMAGES | PROFILES | STORAGE VOLUMES | STORAGE BUCKETS | NETWORKS | NETWORK ZONES |     DESCRIPTION     | USED BY |
+----------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
| default              | YES    | YES      | YES             | YES             | YES      | YES           | Default Incus project | 19      |
+----------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
| my-project (current) | YES    | NO       | NO              | NO              | YES      | YES           |                     | 0       |
+----------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
```

You can request a different output format by adding the `--format` flag.
See [`incus project list --help`](incus_project_list.md) for more information.

## Switch projects

By default, all commands that you issue in Incus affect the project that you are currently using.
To see which project you are in, use the [`incus project list`](incus_project_list.md) command.

To switch to a different project, enter the following command:

    incus project switch <project_name>

## Target a project

Instead of switching to a different project, you can target a specific project when running a command.
Many Incus commands support the `--project` flag to run an action in a different project.

```{note}
You can target only projects that you have permission for.
```

The following sections give some typical examples where you would typically target a project instead of switching to it.

### List instances in a project

To list the instances in a specific project, add the `--project` flag to the [`incus list`](incus_list.md) command.
For example:

    incus list --project my-project

### Move an instance to another project

To move an instance from one project to another, enter the following command:

    incus move <instance_name> <new_instance_name> --project <source_project> --target-project <target_project>

You can keep the same instance name if no instance with that name exists in the target project.

For example, to move the instance `my-instance` from the `default` project to `my-project` and keep the instance name, enter the following command:

    incus move my-instance my-instance --project default --target-project my-project

### Copy a profile to another project

If you create a project with the default settings, profiles are isolated in the project ([`features.profiles`](project-features) is set to `true`).
Therefore, the project does not have access to the default profile (which is part of the `default` project), and you will see an error similar to the following when trying to create an instance:

```{terminal}
:input: incus launch images:ubuntu/22.04 my-instance

Creating my-instance
Error: Failed instance creation: Failed creating instance record: Failed initialising instance: Failed getting root disk: No root device could be found
```

To fix this, you can copy the contents of the `default` project's default profile into the current project's default profile.
To do so, enter the following command:

    incus profile show default --project default | incus profile edit default
