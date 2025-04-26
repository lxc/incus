(devices-infiniband)=
# Type: `infiniband`

```{note}
The `infiniband` device type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

Incus supports two different kinds of network types for InfiniBand devices:

- `physical`: Passes a physical device from the host through to the instance.
  The targeted device will vanish from the host and appear in the instance.
- `sriov`: Passes a virtual function of an SR-IOV-enabled physical network device into the instance.

  ```{note}
  InfiniBand devices support SR-IOV, but in contrast to other SR-IOV-enabled devices, InfiniBand does not support dynamic device creation in SR-IOV mode.
  Therefore, you must pre-configure the number of virtual functions by configuring the corresponding kernel module.
  ```

To create a `physical` `infiniband` device, use the following command:

    incus config device add <instance_name> <device_name> infiniband nictype=physical parent=<device>

To create an `sriov` `infiniband` device, use the following command:

    incus config device add <instance_name> <device_name> infiniband nictype=sriov parent=<sriov_enabled_device>

## Device options

`infiniband` devices have the following device options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-infiniband start -->
    :end-before: <!-- config group devices-infiniband end -->
```
