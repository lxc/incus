(containers-and-vms)=
# About containers and VMs

Incus provides support for two different types of {ref}`instances <expl-instances>`: *system containers* and *virtual machines*.

Incus uses features of the Linux kernel (such as `namespaces` and `cgroups`) in the implementation of system containers. These features provide a software-only way to isolate and restrict a running system container. A system container can only be based on the Linux kernel.

When running a virtual machine, Incus uses hardware features of the the host system as a way to isolate and restrict a running virtual machine. Therefore, virtual machines can be used to run, for example, different operating systems than the host system.

| Virtual Machines                  | Application Containers      | System Containers                 |
| :--:                              | :--:                        | :--:                              |
| Uses a dedicated kernel           | Uses the kernel of the host | Uses the kernel of the host       |
| Can host different types of OS    | Can only host Linux         | Can only host Linux               |
| Uses more resources               | Uses less resources         | Uses less resources               |
| Requires hardware virtualization  | Software-only               | Software-only                     |
| Can host multiple applications    | Can host a single app       | Can host multiple applications    |
| Supported by Incus                | Supported by Docker         | Supported by Incus                |

## Application containers vs. system containers

Application containers (as provided by, for example, Docker) package a single process or application. System containers, on the other hand, simulate a full operating system similar to what you would be running on a host or in a virtual machine. You can run Docker in an Incus system container, but you would not run Incus in a Docker application container.

Therefore, application containers are suitable to provide separate components, while system containers provide a full solution of libraries, applications, databases and so on. In addition, you can use system containers to create different user spaces and isolate all processes belonging to each user space, which is not what application containers are intended for.

![Application and system containers](/images/application-vs-system-containers.svg "Application and system containers")

## Virtual machines vs. system containers

Virtual machines create a virtual version of a physical machine, using hardware features of the host system. The boundaries between the host system and virtual machines is enforced by those hardware features. System containers, on the other hand, use the already running OS kernel of the host system instead of launching their own kernel. If you run several system containers, they all share the same kernel, which makes them faster and more lightweight than virtual machines.

With Incus, you can create both system containers and virtual machines. You should use a system container to leverage the smaller size and increased performance if all functionality you require is compatible with the kernel of your host operating system. If you need functionality that is not supported by the OS kernel of your host system or you want to run a completely different OS, use a virtual machine.

![Virtual machines and system containers](/images/virtual-machines-vs-system-containers.svg "Virtual machines and system containers")
