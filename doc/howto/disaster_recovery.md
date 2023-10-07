(disaster-recovery)=
# How to recover instances in case of disaster

Incus provides a tool for disaster recovery in case the {ref}`Incus database <database>` is corrupted or otherwise lost.

The tool scans the storage pools for instances and imports the instances that it finds back into the database.
You need to re-create the required entities that are missing (usually profiles, projects, and networks).

```{important}
This tool should be used for disaster recovery only.
Do not rely on this tool as an alternative to proper backups; you will lose data like profiles, network definitions, or server configuration.

The tool must be run interactively and cannot be used in automated scripts.
```

## Recovery process

When you run the tool, it scans all storage pools that still exist in the database, looking for missing volumes that can be recovered.
You can also specify the details of any unknown storage pools (those that exist on disk but do not exist in the database), and the tool attempts to scan those too.

After mounting the specified storage pools (if not already mounted), the tool scans them for unknown volumes that look like they are associated with Incus.
Incus maintains a `backup.yaml` file in each instance's storage volume, which contains all necessary information to recover a given instance (including instance configuration, attached devices, storage volume, and pool configuration).
This data can be used to rebuild the instance, storage volume, and storage pool database records.
Before recovering an instance, the tool performs some consistency checks to compare what is in the `backup.yaml` file with what is actually on disk (such as matching snapshots).
If all checks out, the database records are re-created.

If the storage pool database record also needs to be created, the tool uses the information from an instance's `backup.yaml` file as the basis of its configuration, rather than what the user provided during the discovery phase.
However, if this information is not available, the tool falls back to restoring the pool's database record with what was provided by the user.

The tool asks you to re-create missing entities like networks.
However, the tool does not know how the instance was configured.
That means that if some configuration was specified through the `default` profile, you must also re-add the required configuration to the profile.
For example, if the `incusbr0` bridge is used in an instance and you are prompted to re-create it, you must add it back to the `default` profile so that the recovered instance uses it.

## Example

This is how a recovery process could look:

```{terminal}
:input: incus admin recover

This Incus server currently has the following storage pools:
Would you like to recover another storage pool? (yes/no) [default=no]: yes
Name of the storage pool: default
Name of the storage backend (btrfs, ceph, cephfs, cephobject, dir, lvm, zfs): zfs
Source of the storage pool (block device, volume group, dataset, path, ... as applicable): /var/lib/incus/storage-pools/default/containers
Additional storage pool configuration property (KEY=VALUE, empty when done): zfs.pool_name=default
Additional storage pool configuration property (KEY=VALUE, empty when done):
Would you like to recover another storage pool? (yes/no) [default=no]:
The recovery process will be scanning the following storage pools:
 - NEW: "default" (backend="zfs", source="/var/lib/incus/storage-pools/default/containers")
Would you like to continue with scanning for lost volumes? (yes/no) [default=yes]: yes
Scanning for unknown volumes...
The following unknown volumes have been found:
 - Container "u1" on pool "default" in project "default" (includes 0 snapshots)
 - Container "u2" on pool "default" in project "default" (includes 0 snapshots)
You are currently missing the following:
 - Network "incusbr0" in project "default"
Please create those missing entries and then hit ENTER: ^Z
[1]+  Stopped                 incus admin recover
:input: incus network create incusbr0
Network incusbr0 created
:input: fg
incus admin recover

The following unknown volumes have been found:
 - Container "u1" on pool "default" in project "default" (includes 0 snapshots)
 - Container "u2" on pool "default" in project "default" (includes 0 snapshots)
Would you like those to be recovered? (yes/no) [default=no]: yes
Starting recovery...
:input: incus list
+------+---------+------+------+-----------+-----------+
| NAME |  STATE  | IPV4 | IPV6 |   TYPE    | SNAPSHOTS |
+------+---------+------+------+-----------+-----------+
| u1   | STOPPED |      |      | CONTAINER | 0         |
+------+---------+------+------+-----------+-----------+
| u2   | STOPPED |      |      | CONTAINER | 0         |
+------+---------+------+------+-----------+-----------+
:input: incus profile device add default eth0 nic network=incusbr0 name=eth0
Device eth0 added to default
:input: incus start u1
:input: incus list
+------+---------+-------------------+---------------------------------------------+-----------+-----------+
| NAME |  STATE  |       IPV4        |                    IPV6                     |   TYPE    | SNAPSHOTS |
+------+---------+-------------------+---------------------------------------------+-----------+-----------+
| u1   | RUNNING | 192.0.2.49 (eth0) | 2001:db8:8b6:abfe:216:3eff:fe82:918e (eth0) | CONTAINER | 0         |
+------+---------+-------------------+---------------------------------------------+-----------+-----------+
| u2   | STOPPED |                   |                                             | CONTAINER | 0         |
+------+---------+-------------------+---------------------------------------------+-----------+-----------+
```
