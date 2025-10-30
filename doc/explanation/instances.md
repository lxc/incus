(expl-instances)=
# About instances

Incus supports the following types of instances:

Systems Containers
: System containers run full Linux distributions using a shared kernel.
  Those containers run a full Linux distribution, very similar to a virtual machine but sharing kernel with the host system.

  They have an extremely low overhead, can be packed very densely and
  generally provide a near identical experience to virtual machines
  without the required hardware support and overhead.

  System containers are implemented through the use of `liblxc` (LXC).

Virtual machines
: {abbr}`VMs (Virtual machines)` are a full virtualized system.
  Virtual machines are also natively supported by Incus and provide an alternative to system containers.

  Incus uses `qemu` to provide the VM functionality.

  ```{note}
  Currently, virtual machines support fewer features than containers, but the plan is to support the same set of features for both instance types in the future.

  To see which features are available for virtual machines, check the condition column in the {ref}`instance-options` documentation.
  ```

See {ref}`containers-and-vms` for more information about the different instance types.
