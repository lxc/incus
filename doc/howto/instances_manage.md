(instances-manage)=
# How to manage instances

Enter the following command to list all instances:

    lxc list

You can filter the instances that are displayed, for example, by type, status or the cluster member where the instance is located:

    lxc list type=container
    lxc list status=running
    lxc list location=server1

You can also filter by name.
To list several instances, use a regular expression for the name.
For example:

    lxc list ubuntu.*

Enter `lxc list --help` to see all filter options.

## Show information about an instance

Enter the following command to show detailed information about an instance:

    lxc info <instance_name>

Add `--show-log` to the command to show the latest log lines for the instance:

    lxc info <instance_name> --show-log

## Start an instance

Enter the following command to start an instance:

    lxc start <instance_name>

You will get an error if the instance does not exist or if it is running already.

To immediately attach to the console when starting, pass the `--console` flag.
For example:

    lxc start <instance_name> --console

See {ref}`instances-console` for more information.

(instances-manage-stop)=
## Stop an instance

Enter the following command to stop an instance:

    lxc stop <instance_name>

You will get an error if the instance does not exist or if it is not running.

## Delete an instance

If you don't need an instance anymore, you can remove it.
The instance must be stopped before you can delete it.

Enter the following command to delete an instance:

    lxc delete <instance_name>

```{caution}
This command permanently deletes the instance and all its snapshots.
```

### Prevent accidental deletion of instances

There are two ways to prevent accidental deletion of instances:

- To be prompted for approval every time you use the `lxc delete` command, create an alias for it:

       lxc alias add delete "delete -i"

- To protect a specific instance from being deleted, set {config:option}`instance-security:security.protection.delete` to `true` for the instance.
  See {ref}`instances-configure` for instructions.

## Rebuild an instance

If you want to wipe and re-initialize the root disk of your instance but keep the instance configuration, you can rebuild the instance.

Rebuilding is only possible for instances that do not have any snapshots.

Stop your instance before rebuilding it.
Then enter one of the following commands:

- Rebuild the instance with a different image:

        lxc rebuild <image_name> <instance_name>

- Rebuild the instance with an empty root disk:

        lxc rebuild <instance_name> --empty

For more information about the `rebuild` command, see `lxc rebuild --help`.
