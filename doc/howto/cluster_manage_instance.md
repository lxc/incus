(cluster-manage-instance)=
# How to manage instances in a cluster

In a cluster setup, each instance lives on one of the cluster members.
You can operate each instance from any cluster member, so you do not need to log on to the cluster member on which the instance is located.

(cluster-target-instance)=
## Launch an instance on a specific cluster member

When you launch an instance, you can target it to run on a specific cluster member.
You can do this from any cluster member.

For example, to launch an instance named `c1` on the cluster member `server2`, use the following command:

    incus launch images:ubuntu/22.04 c1 --target server2

You can launch instances on specific cluster members or on specific {ref}`cluster groups <howto-cluster-groups>`.

If you do not specify a target, the instance is assigned to a cluster member automatically.
See {ref}`clustering-instance-placement` for more information.

## Check where an instance is located

To check on which member an instance is located, list all instances in the cluster:

    incus list

The location column indicates the member on which each instance is running.

## Move an instance

You can move an existing instance to another cluster member.
For example, to move the instance `c1` to the cluster member `server1`, use the following commands:

    incus stop c1
    incus move c1 --target server1
    incus start c1

See {ref}`move-instances` for more information.

To move an instance to a member of a cluster group, use the group name prefixed with `@` for the `--target` flag.
For example:

    incus move c1 --target @group1
