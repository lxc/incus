(instances-troubleshoot)=
# How to troubleshoot failing instances

If your instance fails to start and ends up in an error state, this usually indicates a bigger issue related to either the image that you used to create the instance or the server configuration.

To troubleshoot the problem, complete the following steps:

1. Save the relevant log files and debug information:

   Instance log
   : Enter the following command to display the instance log:

         incus info <instance_name> --show-log

   Console log
   : Enter the following command to display the console log:

         incus console <instance_name> --show-log

1. Reboot the machine that runs your Incus server.
1. Try starting your instance again.
   If the error occurs again, compare the logs to check if it is the same error.

   If it is, and if you cannot figure out the source of the error from the log information, open a question in the [forum](https://discuss.linuxcontainers.org).
   Make sure to include the log files you collected.

## Troubleshooting example

In this example, let's investigate a RHEL 7 system in which `systemd` cannot start.

```{terminal}
:input: incus console --show-log systemd

Console log:

Failed to insert module 'autofs4'
Failed to insert module 'unix'
Failed to mount sysfs at /sys: Operation not permitted
Failed to mount proc at /proc: Operation not permitted
[!!!!!!] Failed to mount API filesystems, freezing.
```

The errors here say that `/sys` and `/proc` cannot be mounted - which is correct in an unprivileged container.
However, Incus mounts these file systems automatically if it can.

The {doc}`container requirements <../container-environment>` specify that every container must come with an empty `/dev`, `/proc` and `/sys` directory, and that `/sbin/init` must exist.
If those directories don't exist, Incus cannot mount them, and `systemd` will then try to do so.
As this is an unprivileged container, `systemd` does not have the ability to do this, and it then freezes.

So you can see the environment before anything is changed, and you can explicitly change the init system in a container using the `raw.lxc` configuration parameter.
This is equivalent to setting `init=/bin/bash` on the Linux kernel command line.

    incus config set systemd raw.lxc 'lxc.init.cmd = /bin/bash'

Here is what it looks like:

```{terminal}
:input: incus config set systemd raw.lxc 'lxc.init.cmd = /bin/bash'

:input: incus start systemd
:input: incus console --show-log systemd

Console log:

[root@systemd /]#
```

Now that the container has started, you can check it and see that things are not running as well as expected:

```{terminal}
:input: incus exec systemd bash

[root@systemd ~]# ls
[root@systemd ~]# mount
mount: failed to read mtab: No such file or directory
[root@systemd ~]# cd /
[root@systemd /]# ls /proc/
sys
[root@systemd /]# exit
```

Because Incus tries to auto-heal, it created some of the directories when it was starting up.
Shutting down and restarting the container fixes the problem, but the original cause is still there - the template does not contain the required files.
