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

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`gid`       | int       | `0`               | GID of the device owner in the instance (container only)
`id`        | string    | -                 | The DRM card ID of the GPU device
`mode`      | int       | `0660`            | Mode of the device in the instance (container only)
`pci`       | string    | -                 | The PCI address of the GPU device
`productid` | string    | -                 | The product ID of the GPU device
`uid`       | int       | `0`               | UID of the device owner in the instance (container only)
`vendorid`  | string    | -                 | The vendor ID of the GPU device

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

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`id`        | string    | -                 | The DRM card ID of the GPU device
`mdev`      | string    | -                 | The `mdev` profile to use (required - for example, `i915-GVTg_V5_4`)
`pci`       | string    | -                 | The PCI address of the GPU device
`productid` | string    | -                 | The product ID of the GPU device
`vendorid`  | string    | -                 | The vendor ID of the GPU device

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

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`id`        | string    | -                 | The DRM card ID of the GPU device
`mig.ci`    | int       | -                 | Existing MIG compute instance ID
`mig.gi`    | int       | -                 | Existing MIG GPU instance ID
`mig.uuid`  | string    | -                 | Existing MIG device UUID (`MIG-` prefix can be omitted)
`pci`       | string    | -                 | The PCI address of the GPU device
`productid` | string    | -                 | The product ID of the GPU device
`vendorid`  | string    | -                 | The vendor ID of the GPU device

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

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`id`         | string   | -                 | The DRM card ID of the parent GPU device
`pci`        | string   | -                 | The PCI address of the parent GPU device
`productid`  | string   | -                 | The product ID of the parent GPU device
`vendorid`   | string   | -                 | The vendor ID of the parent GPU device
