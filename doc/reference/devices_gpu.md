(devices-gpu)=
# Type: `gpu`

GPU devices make the specified GPU device or devices appear in the instance.

```{note}
For containers, a `gpu` device may match multiple GPUs at once.
For VMs, each device can match only a single GPU.
```

The following types of GPUs can be added using the `gputype` device option:

- [`physical`](gpu-physical) (container and VM): Passes an entire GPU through into the instance.
  This value is the default if `gputype` is unspecified.
- [`mdev`](gpu-mdev) (VM only): Creates and passes a virtual GPU through into the instance.
- [`mig`](gpu-mig) (container only): Creates and passes a MIG (Multi-Instance GPU) through into the instance.
- [`sriov`](gpu-sriov) (VM only): Passes a virtual function of an SR-IOV-enabled GPU into the instance.

The available device options depend on the GPU type and are listed in the tables in the following sections.

(gpu-physical)=
## `gputype`: `physical`

```{note}
The `physical` GPU type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

A `physical` GPU device passes an entire GPU through into the instance.

### Device options

GPU devices of type `physical` have the following device options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-gpu_physical start -->
    :end-before: <!-- config group devices-gpu_physical end -->
```

(gpu-mdev)=
## `gputype`: `mdev`

```{note}
The `mdev` GPU type is supported only for VMs.
It does not support hotplugging.
```

An `mdev` GPU device creates and passes a virtual GPU through into the instance.
You can check the list of available `mdev` profiles by running [`incus info --resources`](incus_info.md).

### Device options

GPU devices of type `mdev` have the following device options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-gpu_mdev start -->
    :end-before: <!-- config group devices-gpu_mdev end -->
```

(gpu-mig)=
## `gputype`: `mig`

```{note}
The `mig` GPU type is supported only for containers.
It does not support hotplugging.
```

A `mig` GPU device creates and passes a MIG compute instance through into the instance.
Currently, this requires NVIDIA MIG instances to be pre-created.

### Device options

GPU devices of type `mig` have the following device options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-gpu_mig start -->
    :end-before: <!-- config group devices-gpu_mig end -->
```

You must set either `mig.uuid` (NVIDIA drivers 470+) or both `mig.ci` and `mig.gi` (old NVIDIA drivers).

(gpu-sriov)=
## `gputype`: `sriov`

```{note}
The `sriov` GPU type is supported only for VMs.
It does not support hotplugging.
```

An `sriov` GPU device passes a virtual function of an SR-IOV-enabled GPU into the instance.

### Device options

GPU devices of type `sriov` have the following device options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-gpu_sriov start -->
    :end-before: <!-- config group devices-gpu_sriov end -->
```
