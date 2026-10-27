(howto-storage-create-instance)=
# How to create an instance in a specific storage pool

Instance storage volumes are created in the storage pool that is specified by the instance's root disk device.
This configuration is normally provided by the profile or profiles applied to the instance.
See {ref}`storage-default-pool` for detailed information.

To use a different storage pool when creating or launching an instance, add the `--storage` flag.
This flag overrides the root disk device from the profile.
For example:

    incus launch <image> <instance_name> --storage <storage_pool>

% Include content from [storage_move_volume.md](storage_move_volume.md)
```{include} storage_move_volume.md
    :start-after: (storage-move-instance)=
```
