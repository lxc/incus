(cluster-recover)=
# How to recover a cluster

It might happen that one or several members of your cluster go offline or become unreachable.
In that case, no operations are possible on this member, and neither are operations that require a state change across all members.
See {ref}`clustering-offline-members` and {ref}`cluster-automatic-evacuation` for more information.

If you can bring the offline cluster members back or delete them from the cluster, operation resumes as normal.
If this is not possible, there are a few ways to recover the cluster, depending on the scenario that caused the failure.
See the following sections for details.

```{note}
Run `incus admin cluster --help` for an overview of all available commands.
```

## Recover from quorum loss

Every Incus cluster has a specific number of members (configured through {config:option}`server-cluster:cluster.max_voters`) that serve as voting members of the distributed database.
If you permanently lose a majority of these cluster members (for example, you have a three-member cluster and you lose two members), the cluster loses quorum and becomes unavailable.
However, if at least one database member survives, it is possible to recover the cluster.

To do so, complete the following steps:

1. Log on to any surviving member of your cluster and run the following command:

       sudo incus admin cluster list-database

   This command shows which cluster members have one of the database roles.
1. Pick one of the listed database members that is still online as the new leader.
   Log on to the machine (if it differs from the one you are already logged on to).
1. Make sure that the Incus daemon is not running on the machine.

       sudo systemctl stop incus.service incus.socket

1. Log on to all other cluster members that are still online and stop the Incus daemon.
1. On the server that you picked as the new leader, run the following command:

       sudo incus admin cluster recover-from-quorum-loss

1. Start the Incus daemon again on all machines, starting with the new leader.

       sudo systemctl start incus.socket incus.service

The database should now be back online.
No information has been deleted from the database.
All information about the cluster members that you have lost is still there, including the metadata about their instances.
This can help you with further recovery steps if you need to re-create the lost instances.

To permanently delete the cluster members that you have lost, force-remove them.
See {ref}`cluster-manage-delete-members`.

## Recover cluster members with changed addresses

If some members of your cluster are no longer reachable, or if the cluster itself is unreachable due to a change in IP address or listening port number, you can reconfigure the cluster.

To do so, edit the cluster configuration on each member of the cluster and change the IP addresses or listening port numbers as required.
You cannot remove any members during this process.
The cluster configuration must contain the description of the full cluster, so you must do the changes for all cluster members on all cluster members.

You can edit the {ref}`clustering-member-roles` of the different members, but with the following limitations:

- A cluster member that does not have a `database*` role cannot become a voter, because it might lack a global database.
- At least two members must remain voters (except in the case of a two-member cluster, where one voter suffices), or there will be no quorum.

Log on to each cluster member and complete the following steps:

1. Stop the Incus daemon.

       sudo systemctl stop incus.service incus.socket

1. Run the following command:

       sudo incus admin cluster edit

1. Edit the YAML representation of the information that this cluster member has about the rest of the cluster:

   ```yaml
   # Latest cowsql segment ID: 1234

   members:
     - id: 1             # Internal ID of the member (Read-only)
       name: server1     # Name of the cluster member (Read-only)
       address: 192.0.2.10:8443 # Last known address of the member (Writeable)
       role: voter              # Last known role of the member (Writeable)
     - id: 2             # Internal ID of the member (Read-only)
       name: server2     # Name of the cluster member (Read-only)
       address: 192.0.2.11:8443 # Last known address of the member (Writeable)
       role: stand-by           # Last known role of the member (Writeable)
     - id: 3             # Internal ID of the member (Read-only)
       name: server3     # Name of the cluster member (Read-only)
       address: 192.0.2.12:8443 # Last known address of the member (Writeable)
       role: spare              # Last known role of the member (Writeable)
   ```

   You can edit the addresses and the roles.

After doing the changes on all cluster members, start the Incus daemon on all members again.

    sudo systemctl start incus.socket incus.service

The cluster should now be fully available again with all members reporting in.
No information has been deleted from the database.
All information about the cluster members and their instances is still there.

## Manually alter Raft membership

In some situations, you might need to manually alter the Raft membership configuration of the cluster because of some unexpected behavior.

For example, if you have a cluster member that was removed uncleanly, it might not show up in [`incus cluster list`](incus_cluster_list.md) but still be part of the Raft configuration.
To see the Raft configuration, run the following command:

    incus admin sql local "SELECT * FROM raft_nodes"

In that case, run the following command to remove the leftover node:

    incus admin cluster remove-raft-node <address>
