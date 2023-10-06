(expl-instances)=
# About instances

Incus supports the following types of instances:

Containers
: Containers are the default type for instances.
  They are currently the most complete implementation of Incus instances and support more features than virtual machines.

  Containers are implemented through the use of `liblxc` (LXC).

Virtual machines
: {abbr}`Virtual machines (VMs)` are natively supported since version 4.0 of Incus.
  Thanks to a built-in agent, they can be used almost like containers.

  Incus uses `qemu` to provide the VM functionality.

  ```{note}
  Currently, virtual machines support fewer features than containers, but the plan is to support the same set of features for both instance types in the future.

  To see which features are available for virtual machines, check the condition column in the {ref}`instance-options` documentation.
  ```

See {ref}`containers-and-vms` for more information about the different instance types.
