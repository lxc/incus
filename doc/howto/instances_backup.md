---
myst:
  substitutions:
    type: "instance"
---

(instances-backup)=
# How to back up instances

There are different ways of backing up your instances:

- {ref}`instances-snapshots`
- {ref}`instances-backup-export`
- {ref}`instances-backup-copy`

% Include content from [storage_backup_volume.md](storage_backup_volume.md)
```{include} storage_backup_volume.md
    :start-after: <!-- Include start backup types -->
    :end-before: <!-- Include end backup types -->
```

```{note}
Custom storage volumes might be attached to an instance, but they are not part of the instance.
Therefore, the content of a custom storage volume is not stored when you back up your instance.
You must back up the data of your storage volume separately.
See {ref}`howto-storage-backup-volume` for instructions.
```

(instances-snapshots)=
## Use snapshots for instance backup

You can save your instance at a point in time by creating an instance snapshot, which makes it easy to restore the instance to a previous state.

Instance snapshots are stored in the same storage pool as the instance volume itself.

% Include content from [storage_backup_volume.md](storage_backup_volume.md)
```{include} storage_backup_volume.md
    :start-after: <!-- Include start optimized snapshots -->
    :end-before: <!-- Include end optimized snapshots -->
```

### Create a snapshot

Use the following command to create a snapshot of an instance:

    incus snapshot create <instance_name> [<snapshot name>]

% Include content from [storage_backup_volume.md](storage_backup_volume.md)
```{include} storage_backup_volume.md
    :start-after: <!-- Include start create snapshot options -->
    :end-before: <!-- Include end create snapshot options -->
```

For virtual machines, you can add the `--stateful` flag to capture not only the data included in the instance volume but also the running state of the instance.
Note that this feature is not fully supported for containers because of CRIU limitations.

### View, edit or delete snapshots

Use the following command to display the snapshots for an instance:

    incus info <instance_name>

You can view or modify snapshots in a similar way to instances, by referring to the snapshot with `<instance_name>/<snapshot_name>`.

To show configuration information about a snapshot, use the following command:

    incus config show <instance_name>/<snapshot_name>

To change the expiry date of a snapshot, use the following command:

    incus config edit <instance_name>/<snapshot_name>

```{note}
In general, snapshots cannot be edited, because they preserve the state of the instance.
The only exception is the expiry date.
Other changes to the configuration are silently ignored.
```

To delete a snapshot, use the following command:

    incus delete <instance_name>/<snapshot_name>

### Schedule instance snapshots

You can configure an instance to automatically create snapshots at specific times (at most once every minute).
To do so, set the {config:option}`instance-snapshots:snapshots.schedule` instance option.

For example, to configure daily snapshots, use the following command:

    incus config set <instance_name> snapshots.schedule @daily

To configure taking a snapshot every day at 6 am, use the following command:

    incus config set <instance_name> snapshots.schedule "0 6 * * *"

When scheduling regular snapshots, consider setting an automatic expiry ({config:option}`instance-snapshots:snapshots.expiry`) and a naming pattern for snapshots ({config:option}`instance-snapshots:snapshots.pattern`).
You should also configure whether you want to take snapshots of instances that are not running ({config:option}`instance-snapshots:snapshots.schedule.stopped`).

### Restore an instance snapshot

You can restore an instance to any of its snapshots.

To do so, use the following command:

    incus snapshot restore <instance_name> <snapshot_name>

If the snapshot is stateful (which means that it contains information about the running state of the instance), you can add the `--stateful` flag to restore the state.

(instances-backup-export)=
## Use export files for instance backup

You can export the full content of your instance to a standalone file that can be stored at any location.
For highest reliability, store the backup file on a different file system to ensure that it does not get lost or corrupted.

### Export an instance

Use the following command to export an instance to a compressed file (for example, `/path/to/my-instance.tgz`):

    incus export <instance_name> [<file_path>]

If you do not specify a file path, the export file is saved as `<instance_name>.<extension>` in the working directory (for example, `my-container.tar.gz`).

```{warning}
If the output file (`<instance_name>.<extension>` or the specified file path) already exists, the command overwrites the existing file without warning.
```

% Include content from [storage_backup_volume.md](storage_backup_volume.md)
```{include} storage_backup_volume.md
    :start-after: <!-- Include start export info -->
    :end-before: <!-- Include end export info -->
```

`--instance-only`
: By default, the export file contains all snapshots of the instance.
  Add this flag to export the instance without its snapshots.

### Restore an instance from an export file

You can import an export file (for example, `/path/to/my-backup.tgz`) as a new instance.
To do so, use the following command:

    incus import <file_path> [<instance_name>]

If you do not specify an instance name, the original name of the exported instance is used for the new instance.
If an instance with that name already (or still) exists in the specified storage pool, the command returns an error.
In that case, either delete the existing instance before importing the backup or specify a different instance name for the import.

(instances-backup-copy)=
## Copy an instance to a backup server

You can copy an instance to a secondary backup server to back it up.

See {ref}`move-instances` for instructions.
