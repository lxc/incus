(devices-pci)=
# Type: `pci`

```{note}
The `pci` device type is supported for VMs.
It does not support hotplugging.
```

PCI devices are used to pass raw PCI devices from the host into a virtual machine.

They are mainly intended to be used for specialized single-function PCI cards like sound cards or video capture cards.
In theory, you can also use them for more advanced PCI devices like GPUs or network cards, but it's usually more convenient to use the specific device types that Incus provides for these devices ([`gpu` device](devices-gpu) or [`nic` device](devices-nic)).

## Device options

`pci` devices have the following device options:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`address`           | string    | -         | yes       | PCI address of the device
