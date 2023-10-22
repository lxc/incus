(containers-and-vms)=
# About containers and VMs

Incus provides support for two different types of {ref}`instances <expl-instances>`: *system containers* and *virtual machines*.

When running a system container, Incus simulates a virtual version of a full operating system.  
To do this, it uses the functionality provided by the kernel running on the host system.

When running a virtual machine, Incus uses the hardware of the host system, and the kernel is provided by the virtual machine.  
Therefore, virtual machines can be used to run, for example, a different operating system.
 
|    Virtual Machines              |  Application Containers          |     System Containers    |      
|      :----:                     |      :----:                     |        :----:           |
| Uses a dedicated kernel                 |  Uses the kernel of the host            |   Uses the kernel of the host   |    
| Can host different types of OS  |  Can only host Linux            |  Can only host Linux    |   
| Uses more ressources            |  Uses less ressources           |  Uses less resources    |   
| Can host multiple applications         |  Can host a single app |  Can host multiple applications |  
| Supported by Incus              |  Supported by Docker            |  Supported by Incus     |  

## Application containers and system containers

Application containers like Docker package a single process or application.

System containers, on the other hand, simulate a full operating system and let you run multiple processes at the same time.  
Incus does not provide application containers, and Docker does not provide anything but application containers.

Therefore, application containers are suitable for providing separate components, while system containers provide a full solution of libraries, applications, databases, and so on. In addition, you can use system containers to create different user spaces and isolate all processes belonging to each user space, which is not what application containers are intended for.

![Application and system containers](/images/application-vs-system-containers.svg "Application and system containers")

## Virtual machines and system containers

Virtual machines emulate a physical machine, using the hardware of the host system from a full and completely isolated operating system. System containers, on the other hand, use the OS kernel of the host system instead of creating their own environment. If you run several system containers, they all share the same kernel, which makes them faster and more lightweight than virtual machines.

With Incus, you can create both system containers and virtual machines. 

You should use a system container to leverage the smaller size and the increased performance if all functionality you require is compatible with the kernel of your host operating system. If you need functionality that is not supported by the OS kernel of your host system or you want to run a completely different OS, use a virtual machine.

![Virtual machines and system containers](/images/virtual-machines-vs-system-containers.svg "Virtual machines and system containers")
