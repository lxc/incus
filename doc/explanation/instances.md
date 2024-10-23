(expl-instances)=
# About instances

Incus supports the following types of instances:

Systems Containers
: System containers run full Linux distributions using a shared kernel
  Those containers run a full Linux distribution, very similar to a virtual machine but sharing kernel with the host system.

  They have an extremely low overhead, can be packed very densely and
  generally provide a near identical experience to virtual machines
  without the required hardware support and overhead.

  System containers are implemented through the use of `liblxc` (LXC).

Application containers
: Application containers run a single application through a pre-built image
  Those kind of containers got popularized by the likes of Docker and Kubernetes.

  Rather than provide a pristine Linux environment on top of which software needs to be installed,
  they instead come with a pre-installed and mostly pre-configured piece of software.

  Incus can consume application container images from any OCI-compatible image registry (e.g. the Docker Hub).

  Application containers are implemented through the use of `liblxc` (LXC) with help from `umoci` and `skopeo`.

Virtual machines
: {abbr}`Virtual machines (VMs)` are a full virtualized system
  Virtual machines are also natively supported by Incus and provide an alternative to system containers.

  Not everything can run properly in containers. Anything that requires
  a different kernel or its own kernel modules should be run in a virtual
  machine instead.

  Similarly, some kind of device pass-through, such as full PCI devices will only work properly with virtual machines.

  To keep the user experience consistent, a built-in agent is provided by Incus to allow for interactive command execution and file transfers.

  Virtual machines are implemented through the use of QEMU.

  ```{note}
  Currently, virtual machines support fewer features than containers, but the plan is to support the same set of features for both instance types in the future.

  To see which features are available for virtual machines, check the condition column in the {ref}`instance-options` documentation.
  ```

See {ref}`containers-and-vms` for more information about the different instance types.
