(backups)=
# How to back up a Incus server

In a production setup, you should always back up the contents of your Incus server.

The Incus server contains a variety of different entities, and when choosing your backup strategy, you must decide which of these entities you want to back up and how frequently you want to save them.

## What to back up

The various contents of your Incus server are located on your file system and, in addition, recorded in the {ref}`Incus database <database>`.
Therefore, only backing up the database or only backing up the files on disk does not give you a full functional backup.

Your Incus server contains the following entities:

- Instances (database records and file systems)
- Images (database records, image files, and file systems)
- Networks (database records and state files)
- Profiles (database records)
- Storage volumes (database records and file systems)

Consider which of these you need to back up.
For example, if you don't use custom images, you don't need to back up your images since they are available on the image server.
If you use only the `default` profile, or only the standard `incusbr0` network bridge, you might not need to worry about backing them up, because they can easily be re-created.

## Full backup

To create a full backup of all contents of your Incus server, back up the `/var/lib/incus` directory.

This directory contains your local storage, the Incus database, and your configuration.
It does not contain separate storage devices, however.
That means that whether the directory also contains the data of your instances depends on the storage drivers that you use.

```{important}
If your Incus server uses any external storage (for example, LVM volume groups, ZFS zpools, or any other resource that isn't directly self-contained to Incus), you must back this up separately.

See {ref}`howto-storage-backup-volume` for instructions.
```

To back up your data, create a tarball of `/var/lib/incus`.
If your system uses `/etc/subuid` and `/etc/subgid` file, you should also back up these files.
Restoring them avoids needless shifting of instance file systems.

To restore your data, complete the following steps:

1. Stop Incus on your server (for example, with `sudo systemctl stop incus.service incus.socket`).
1. Delete the directory (`/var/lib/incus/`).
1. Restore the directory from the backup.
1. Delete and restore any external storage devices.
1. Restore the `/etc/subuid` and `/etc/subgid` files if present.
1. Restart Incus (for example, with `sudo systemctl start incus.socket incus.service` or by restarting your machine).

## Partial backup

If you decide to only back up specific entities, you have different options for how to do this.
You should consider doing some of these partial backups even if you are doing full backups in addition.
It can be easier and safer to, for example, restore a single instance or reconfigure a profile than to restore the full Incus server.

### Back up instances and volumes

Instances and storage volumes are backed up in a very similar way (because when backing up an instance, you basically back up its instance volume, see {ref}`storage-volume-types`).

See {ref}`instances-backup` and {ref}`howto-storage-backup-volume` for detailed information.
The following sections give a brief summary of the options you have for backing up instances and volumes.

#### Secondary backup Incus server

Incus supports copying and moving instances and storage volumes between two hosts.
See {ref}`move-instances` and {ref}`howto-storage-move-volume` for instructions.

So if you have a spare server, you can regularly copy your instances and storage volumes to that secondary server to back them up.
If needed, you can either switch over to the secondary server or copy your instances or storage volumes back from it.

If you use the secondary server as a pure storage server, it doesn't need to be as powerful as your main Incus server.

#### Export tarballs

You can use the `export` command to export instances and volumes to a backup tarball.
By default, those tarballs include all snapshots.

You can use an optimized export option, which is usually quicker and results in a smaller size of the tarball.
However, you must then use the same storage driver when restoring the backup tarball.

See {ref}`instances-backup-export` and {ref}`storage-backup-export` for instructions.

#### Snapshots

Snapshots save the state of an instance or volume at a specific point in time.
However, they are stored in the same storage pool and are therefore likely to be lost if the original data is deleted or lost.
This means that while snapshots are very quick and easy to create and restore, they don't constitute a secure backup.

See {ref}`instances-snapshots` and {ref}`storage-backup-snapshots` for more information.

(backup-database)=
### Back up the database

While there is no trivial method to restore the contents of the {ref}`Incus database <database>`, it can still be very convenient to keep a backup of its content.
Such a backup can make it much easier to re-create, for example, networks or profiles if the need arises.

Use the following command to dump the content of the local database to a file:

    incus admin sql local .dump > <output_file>

Use the following command to dump the content of the global database to a file:

    incus admin sql global .dump > <output_file>

You should include these two commands in your regular Incus backup.
