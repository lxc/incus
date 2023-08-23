---
discourse: 9223
---

(instances-console)=
# How to access the console

Use the [`lxc console`](lxc_console.md) command to attach to instance consoles.
The console is available at boot time already, so you can use it to see boot messages and, if necessary, debug startup issues of a container or VM.

To get an interactive console, enter the following command:

    lxc console <instance_name>

To show log output, pass the `--show-log` flag:

    lxc console <instance_name> --show-log

You can also immediately attach to the console when you start your instance:

    lxc start <instance_name> --console
    lxc start <instance_name> --console=vga

## Access the graphical console (for virtual machines)

```{youtube} https://www.youtube.com/watch?v=pEUsTMiq4B4
```

On virtual machines, log on to the console to get graphical output.
Using the console you can, for example, install an operating system using a graphical interface or run a desktop environment.

An additional advantage is that the console is available even if the `lxd-agent` process is not running.
This means that you can access the VM through the console before the `lxd-agent` starts up, and also if the `lxd-agent` is not available at all.

To start the VGA console with graphical output for your VM, you must install a SPICE client (for example, `virt-viewer` or `spice-gtk-client`).
Then enter the following command:

    lxc console <vm_name> --type vga
