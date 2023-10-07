(instances-console)=
# How to access the console

Use the [`incus console`](incus_console.md) command to attach to instance consoles.
The console is available at boot time already, so you can use it to see boot messages and, if necessary, debug startup issues of a container or VM.

To get an interactive console, enter the following command:

    incus console <instance_name>

To show log output, pass the `--show-log` flag:

    incus console <instance_name> --show-log

You can also immediately attach to the console when you start your instance:

    incus start <instance_name> --console
    incus start <instance_name> --console=vga

## Access the graphical console (for virtual machines)

On virtual machines, log on to the console to get graphical output.
Using the console you can, for example, install an operating system using a graphical interface or run a desktop environment.

An additional advantage is that the console is available even if the `incus-agent` process is not running.
This means that you can access the VM through the console before the `incus-agent` starts up, and also if the `incus-agent` is not available at all.

To start the VGA console with graphical output for your VM, you must install a SPICE client (for example, `virt-viewer` or `spice-gtk-client`).
Then enter the following command:

    incus console <vm_name> --type vga
