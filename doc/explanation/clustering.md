(exp-clustering)=
# About clustering

To spread the total workload over several servers, Incus can be run in clustering mode.
In this scenario, any number of Incus servers share the same distributed database that holds the configuration for the cluster members and their instances.
The Incus cluster can be managed uniformly using the [`incus`](incus.md) client or the REST API.

This feature was introduced as part of the [`clustering`](../api-extensions.md#clustering) API extension and is available since Incus 3.0.

```{tip}
If you want to quickly set up a basic Incus cluster, check out [MicroCloud](https://microcloud.is).
```

(clustering-members)=
## Cluster members

A Incus cluster consists of one bootstrap server and at least two further cluster members.
It stores its state in a [distributed database](../database.md), which is a [Cowsql](https://github.com/cowsql/cowsql/) database replicated using the Raft algorithm.

While you could create a cluster with only two members, it is strongly recommended that the number of cluster members be at least three.
With this setup, the cluster can survive the loss of at least one member and still be able to establish quorum for its distributed state.

When you create the cluster, the Cowsql database runs on only the bootstrap server until a third member joins the cluster.
Then both the second and the third server receive a replica of the database.

See {ref}`cluster-form` for more information.

(clustering-member-roles)=
### Member roles

In a cluster with three members, all members replicate the distributed database that stores the state of the cluster.
If the cluster has more members, only some of them replicate the database.
The remaining members have access to the database, but don't replicate it.

At each time, there is an elected cluster leader that monitors the health of the other members.

Each member that replicates the database has either the role of a *voter* or of a *stand-by*.
If the cluster leader goes offline, one of the voters is elected as the new leader.
If a voter member goes offline, a stand-by member is automatically promoted to voter.
The database (and hence the cluster) remains available as long as a majority of voters is online.

The following roles can be assigned to Incus cluster members.
Automatic roles are assigned by Incus itself and cannot be modified by the user.

| Role                  | Automatic     | Description |
| :---                  | :--------     | :---------- |
| `database`            | yes           | Voting member of the distributed database |
| `database-leader`     | yes           | Current leader of the distributed database |
| `database-standby`    | yes           | Stand-by (non-voting) member of the distributed database |
| `event-hub`           | no            | Exchange point (hub) for the internal Incus events (requires at least two) |
| `ovn-chassis`         | no            | Uplink gateway candidate for OVN networks |

The default number of voter members ({config:option}`server-cluster:cluster.max_voters`) is three.
The default number of stand-by members ({config:option}`server-cluster:cluster.max_standby`) is two.
With this configuration, your cluster will remain operational as long as you switch off at most one voting member at a time.

See {ref}`cluster-manage` for more information.

(clustering-offline-members)=
#### Offline members and fault tolerance

If a cluster member is down for more than the configured offline threshold, its status is marked as offline.
In this case, no operations are possible on this member, and neither are operations that require a state change across all members.

As soon as the offline member comes back online, operations are available again.

If the member that goes offline is the leader itself, the other members will elect a new leader.

If you can't or don't want to bring the server back online, you can [delete it from the cluster](cluster-manage-delete-members).

You can tweak the amount of seconds after which a non-responding member is considered offline by setting the {config:option}`server-cluster:cluster.offline_threshold` configuration.
The default value is 20 seconds.
The minimum value is 10 seconds.

To automatically {ref}`evacuate <cluster-evacuate>` instances from an offline member, set the {config:option}`server-cluster:cluster.healing_threshold` configuration to a non-zero value.

See {ref}`cluster-recover` for more information.

#### Failure domains

You can use failure domains to indicate which cluster members should be given preference when assigning roles to a cluster member that has gone offline.
For example, if a cluster member that currently has the database role gets shut down, Incus tries to assign its database role to another cluster member in the same failure domain, if one is available.

To update the failure domain of a cluster member, use the [`incus cluster edit <member>`](incus_cluster_edit.md) command and change the `failure_domain` property from `default` to another string.

(clustering-member-config)=
### Member configuration

Incus cluster members are generally assumed to be identical systems.
This means that all Incus servers joining a cluster must have an identical configuration to the bootstrap server, in terms of storage pools and networks.

To accommodate things like slightly different disk ordering or network interface naming, there is an exception for some configuration options related to storage and networks, which are member-specific.

When such settings are present in a cluster, any server that is being added must provide a value for them.
Most often, this is done through the interactive `incus admin init` command, which asks the user for the value for a number of configuration keys related to storage or networks.

Those settings typically include:

- The source device and size for a storage pool
- The name for a ZFS zpool, LVM thin pool or LVM volume group
- External interfaces and BGP next-hop for a bridged network
- The name of the parent network device for managed `physical` or `macvlan` networks

See {ref}`cluster-config-storage` and {ref}`cluster-config-networks` for more information.

If you want to look up the questions ahead of time (which can be useful for scripting), query the `/1.0/cluster` API endpoint.
This can be done through `incus query /1.0/cluster` or through other API clients.

## Images

By default, Incus replicates images on as many cluster members as there are database members.
This typically means up to three copies within the cluster.

You can increase that number to improve fault tolerance and the likelihood of the image being locally available.
To do so, set the {config:option}`server-cluster:cluster.images_minimal_replica` configuration.
The special value of `-1` can be used to have the image copied to all cluster members.

(cluster-groups)=
## Cluster groups

In a Incus cluster, you can add members to cluster groups.
You can use these cluster groups to launch instances on a cluster member that belongs to a subset of all available members.
For example, you could create a cluster group for all members that have a GPU and then launch all instances that require a GPU on this cluster group.

By default, all cluster members belong to the `default` group.

See {ref}`howto-cluster-groups` and {ref}`cluster-target-instance` for more information.

(clustering-instance-placement)=
## Automatic placement of instances

In a cluster setup, each instance lives on one of the cluster members.
When you launch an instance, you can target it to a specific cluster member, to a cluster group or have Incus automatically assign it to a cluster member.

By default, the automatic assignment picks the cluster member that has the lowest number of instances.
If several members have the same amount of instances, one of the members is chosen at random.

However, you can control this behavior with the {config:option}`cluster-cluster:scheduler.instance` configuration option:

- If `scheduler.instance` is set to `all` for a cluster member, this cluster member is selected for an instance if:

   - The instance is created without `--target` and the cluster member has the lowest number of instances.
   - The instance is targeted to live on this cluster member.
   - The instance is targeted to live on a member of a cluster group that the cluster member is a part of, and the cluster member has the lowest number of instances compared to the other members of the cluster group.

- If `scheduler.instance` is set to `manual` for a cluster member, this cluster member is selected for an instance if:

   - The instance is targeted to live on this cluster member.

- If `scheduler.instance` is set to `group` for a cluster member, this cluster member is selected for an instance if:

   - The instance is targeted to live on this cluster member.
   - The instance is targeted to live on a member of a cluster group that the cluster member is a part of, and the cluster member has the lowest number of instances compared to the other members of the cluster group.

(clustering-instance-placement-scriptlet)=
### Instance placement scriptlet

Incus supports using custom logic to control automatic instance placement by using an embedded script (scriptlet).
This method provides more flexibility than the built-in instance placement functionality.

The instance placement scriptlet must be written in the [Starlark language](https://github.com/bazelbuild/starlark) (which is a subset of Python).
The scriptlet is invoked each time Incus needs to know where to place an instance.
The scriptlet receives information about the instance that is being placed and the candidate cluster members that could host the instance.
It is also possible for the scriptlet to request information about each candidate cluster member's state and the hardware resources available.

An instance placement scriptlet must implement the `instance_placement` function with the following signature:

   `instance_placement(request, candidate_members)`:

- `request` is an object that contains an expanded representation of [`scriptlet.InstancePlacement`](https://pkg.go.dev/github.com/lxc/incus/shared/api/scriptlet/#InstancePlacement). This request includes `project` and `reason` fields. The `reason` can be `new`, `evacuation` or `relocation`.
- `candidate_members` is a `list` of cluster member objects representing [`api.ClusterMember`](https://pkg.go.dev/github.com/lxc/incus/shared/api#ClusterMember) entries.

For example:

```python
def instance_placement(request, candidate_members):
    # Example of logging info, this will appear in Incus' log.
    log_info("instance placement started: ", request)

    # Example of applying logic based on the instance request.
    if request.name == "foo":
        # Example of logging an error, this will appear in Incus' log.
        log_error("Invalid name supplied: ", request.name)

        fail("Invalid name") # Exit with an error to reject instance placement.

    # Place the instance on the first candidate server provided.
    set_target(candidate_members[0].server_name)

    return # Return empty to allow instance placement to proceed.
```

The scriptlet must be applied to Incus by storing it in the `instances.placement.scriptlet` global configuration setting.

For example, if the scriptlet is saved inside a file called `instance_placement.star`, then it can be applied to Incus with the following command:

    cat instance_placement.star | incus config set instances.placement.scriptlet=-

To see the current scriptlet applied to Incus, use the `incus config get instances.placement.scriptlet` command.

The following functions are available to the scriptlet (in addition to those provided by Starlark):

- `log_info(*messages)`: Add a log entry to Incus' log at `info` level. `messages` is one or more message arguments.
- `log_warn(*messages)`: Add a log entry to Incus' log at `warn` level. `messages` is one or more message arguments.
- `log_error(*messages)`: Add a log entry to Incus' log at `error` level. `messages` is one or more message arguments.
- `set_cluster_member_target(member_name)`: Set the cluster member where the instance should be created. `member_name` is the name of the cluster member the instance should be created on. If this function is not called, then Incus will use its built-in instance placement logic.
- `get_cluster_member_state(member_name)`: Get the cluster member's state. Returns an object with the cluster member's state in the form of [`api.ClusterMemberState`](https://pkg.go.dev/github.com/lxc/incus/shared/api#ClusterMemberState). `member_name` is the name of the cluster member to get the state for.
- `get_cluster_member_resources(member_name)`: Get information about resources on the cluster member. Returns an object with the resource information in the form of [`api.Resources`](https://pkg.go.dev/github.com/lxc/incus/shared/api#Resources). `member_name` is the name of the cluster member to get the resource information for.
- `get_instance_resources()`: Get information about the resources the instance will require. Returns an object with the resource information in the form of [`scriptlet.InstanceResources`](https://pkg.go.dev/github.com/lxc/incus/shared/api/scriptlet/#InstanceResources).

```{note}
Field names in the object types are equivalent to the JSON field names in the associated Go types.
```
