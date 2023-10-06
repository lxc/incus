(instances-manage)=
# How to manage instances

When listing the existing instances, you can see their type, status, and location (if applicable).
You can filter the instances and display only the ones that you are interested in.

````{tabs}
```{group-tab} CLI
Enter the following command to list all instances:

    incus list

You can filter the instances that are displayed, for example, by type, status or the cluster member where the instance is located:

    incus list type=container
    incus list status=running
    incus list location=server1

You can also filter by name.
To list several instances, use a regular expression for the name.
For example:

    incus list ubuntu.*

Enter [`incus list --help`](incus_list.md) to see all filter options.
```

```{group-tab} API
Query the `/1.0/instances` endpoint to list all instances.
You can use {ref}`rest-api-recursion` to display more information about the instances:

    incus query /1.0/instances?recursion=2

You can {ref}`filter <rest-api-filtering>` the instances that are displayed, by name, type, status or the cluster member where the instance is located:

    incus query /1.0/instances?filter=name+eq+ubuntu
    incus query /1.0/instances?filter=type+eq+container
    incus query /1.0/instances?filter=status+eq+running
    incus query /1.0/instances?filter=location+eq+server1

To list several instances, use a regular expression for the name.
For example:

    incus query /1.0/instances?filter=name+eq+ubuntu.*

See [`GET /1.0/instances`](swagger:/instances/instances_get) for more information.
```

```{group-tab} UI
Go to {guilabel}`Instances` to see a list of all instances.

You can filter the instances that are displayed by status, instance type, or the profile they use by selecting the corresponding filter.

In addition, you can search for instances by entering a search text.
The text you enter is matched against the name, the description, and the name of the base image.
```
````

## Show information about an instance

````{tabs}
```{group-tab} CLI
Enter the following command to show detailed information about an instance:

    incus info <instance_name>

Add `--show-log` to the command to show the latest log lines for the instance:

    incus info <instance_name> --show-log
```

```{group-tab} API
Query the following endpoint to show detailed information about an instance:

    incus query /1.0/instances/<instance_name>

See [`GET /1.0/instances/{name}`](swagger:/instances/instance_get) for more information.
```

```{group-tab} UI
Clicking an instance line in the overview will show a summary of the instance information right next to the instance list.

Click the instance name to go to the instance detail page, which contains detailed information about the instance.
```
````

## Start an instance

````{tabs}
```{group-tab} CLI
Enter the following command to start an instance:

    incus start <instance_name>

You will get an error if the instance does not exist or if it is running already.

To immediately attach to the console when starting, pass the `--console` flag.
For example:

    incus start <instance_name> --console

See {ref}`instances-console` for more information.
```

```{group-tab} API
To start an instance, send a PUT request to change the instance state:

    incus query --request PUT /1.0/instances/<instance_name>/state --data '{"action":"start"}'

<!-- Include start monitor status -->
The return value of this query contains an operation ID, which you can use to query the status of the operation:

    incus query /1.0/operations/<operation_ID>

Use the following query to monitor the state of the instance:

    incus query /1.0/instances/<instance_name>/state

See [`GET /1.0/instances/{name}/state`](swagger:/instances/instance_state_get) and [`PUT /1.0/instances/{name}/state`](swagger:/instances/instance_state_put)for more information.
<!-- Include end monitor status -->
```

```{group-tab} UI
To start an instance, go to the instance list or the respective instance and click the {guilabel}`Start` button (▷).

You can also start several instances at the same time by selecting them in the instance list and clicking the {guilabel}`Start` button at the top.

On the instance detail page, select the {guilabel}`Console` tab to see the boot log with information about the instance starting up.
Once it is running, you can select the {guilabel}`Terminal` tab to access the instance.
```
````

(instances-manage-stop)=
## Stop an instance

`````{tabs}
````{group-tab} CLI
Enter the following command to stop an instance:

    incus stop <instance_name>

You will get an error if the instance does not exist or if it is not running.
````

````{group-tab} API
To stop an instance, send a PUT request to change the instance state:

    incus query --request PUT /1.0/instances/<instance_name>/state --data '{"action":"stop"}'

% Include content from above
```{include} ./instances_manage.md
    :start-after: <!-- Include start monitor status -->
    :end-before: <!-- Include end monitor status -->
```
````

````{group-tab} UI
To stop an instance, go to the instance list or the respective instance and click the {guilabel}`Stop` button (□).
You are then prompted to confirm.

<!-- Include start skip confirmation -->
```{tip}
To skip the confirmation prompt, hold the {kbd}`Shift` key while clicking.
```
<!-- Include end skip confirmation -->

You can choose to force-stop the instance.
If stopping the instance takes a long time or the instance is not responding to the stop request, click the spinning stop button to go back to the confirmation prompt, where you can select to force-stop the instance.

You can also stop several instances at the same time by selecting them in the instance list and clicking the {guilabel}`Stop` button at the top.
````

`````

## Delete an instance

If you don't need an instance anymore, you can remove it.
The instance must be stopped before you can delete it.

`````{tabs}
```{group-tab} CLI
Enter the following command to delete an instance:

    incus delete <instance_name>
```

```{group-tab} API
To delete an instance, send a DELETE request to the instance:

    incus query --request DELETE /1.0/instances/<instance_name>

See [`DELETE /1.0/instances/{name}`](swagger:/instances/instance_delete) for more information.
```

````{group-tab} UI
To delete an instance, go to its instance detail page and click {guilabel}`Delete instance`.
You are then prompted to confirm.

% Include content from above
```{include} ./instances_manage.md
    :start-after: <!-- Include start skip confirmation -->
    :end-before: <!-- Include end skip confirmation -->
```

You can also delete several instances at the same time by selecting them in the instance list and clicking the {guilabel}`Delete` button at the top.
````
`````

```{caution}
This command permanently deletes the instance and all its snapshots.
```

### Prevent accidental deletion of instances

There are different ways to prevent accidental deletion of instances:

- To protect a specific instance from being deleted, set {config:option}`instance-security:security.protection.delete` to `true` for the instance.
  See {ref}`instances-configure` for instructions.
- In the CLI client, you can create an alias to be prompted for approval every time you use the [`incus delete`](incus_delete.md) command:

       incus alias add delete "delete -i"

## Rebuild an instance

If you want to wipe and re-initialize the root disk of your instance but keep the instance configuration, you can rebuild the instance.

Rebuilding is only possible for instances that do not have any snapshots.

Stop your instance before rebuilding it.

````{tabs}
```{group-tab} CLI
Enter the following command to rebuild the instance with a different image:

    incus rebuild <image_name> <instance_name>

Enter the following command to rebuild the instance with an empty root disk:

    incus rebuild <instance_name> --empty

For more information about the `rebuild` command, see [`incus rebuild --help`](incus_rebuild.md).
```

```{group-tab} API
To rebuild the instance with a different image, send a POST request to the instance's `rebuild` endpoint.
For example:

    incus query --request POST /1.0/instances/<instance_name>/rebuild --data '{"source": {"alias":"<image_alias>","server":"<server_URL>", protocol:"simplestreams"}}'

To rebuild the instance with an empty root disk, specify the source type as `none`:

    incus query --request POST /1.0/instances/<instance_name>/rebuild --data '{"source": {"type":"none"}}'

See [`POST /1.0/instances/{name}/rebuild`](swagger:/instances/instance_rebuild_post) for more information.
```

```{group-tab} UI
Rebuilding an instance is not yet supported in the UI.
```
````
