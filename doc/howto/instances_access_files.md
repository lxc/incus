(instances-access-files)=
# How to access files in an instance

You can manage files inside an instance using the Incus client without needing to access the instance through the network.
Files can be individually edited or deleted, pushed from or pulled to the local machine.
Alternatively, you can mount the instance's file system onto the local machine.

For containers, these file operations always work and are handled directly by Incus.
For virtual machines, the `incus-agent` process must be running inside of the virtual machine for them to work.

## Edit instance files

To edit an instance file from your local machine, enter the following command:

    incus file edit <instance_name>/<path_to_file>

For example, to edit the `/etc/hosts` file in the instance, enter the following command:

    incus file edit my-container/etc/hosts

```{note}
The file must already exist on the instance.
You cannot use the `edit` command to create a file on the instance.
```

## Delete files from the instance

To delete a file from your instance, enter the following command:

    incus file delete <instance_name>/<path_to_file>

## Pull files from the instance to the local machine

To pull a file from your instance to your local machine, enter the following command:

    incus file pull <instance_name>/<path_to_file> <local_file_path>

For example, to pull the `/etc/hosts` file to the current directory, enter the following command:

    incus file pull my-instance/etc/hosts .

Instead of pulling the instance file into a file on the local system, you can also pull it to stdout and pipe it to stdin of another command.
This can be useful, for example, to check a log file:

    incus file pull my-instance/var/log/syslog - | less

To pull a directory with all contents, enter the following command:

    incus file pull -r <instance_name>/<path_to_directory> <local_location>

## Push files from the local machine to the instance

To push a file from your local machine to your instance, enter the following command:

    incus file push <local_file_path> <instance_name>/<path_to_file>

To push a directory with all contents, enter the following command:

    incus file push -r <local_location> <instance_name>/<path_to_directory>

## Mount a file system from the instance

You can mount an instance file system into a local path on your client.

To do so, make sure that you have `sshfs` installed.

    incus file mount <instance_name>/<path_to_directory> <local_location>

You can then access the files from your local machine.

### Set up an SSH SFTP listener

Alternatively, you can set up an SSH SFTP listener.
This method allows you to connect with any SFTP client and with a dedicated user name.

To do so, first set up the listener by entering the following command:

    incus file mount <instance_name> [--listen <address>:<port>]

For example, to set up the listener on a random port on the local machine (for example, `127.0.0.1:45467`):

    incus file mount my-instance

If you want to access your instance files from outside your local network, you can pass a specific address and port:

    incus file mount my-instance --listen 192.0.2.50:2222

```{caution}
Be careful when doing this, because it exposes your instance remotely.
```

To set up the listener on a specific address and a random port:

    incus file mount my-instance --listen 192.0.2.50:0

The command prints out the assigned port and a user name and password for the connection.

```{tip}
You can specify a user name by passing the `--auth-user` flag.
```

Use this information to access the file system.
For example, if you want to use `sshfs` to connect, enter the following command:

    sshfs <user_name>@<address>:<path_to_directory> <local_location> -p <port>

For example:

    sshfs xFn8ai8c@127.0.0.1:/home my-instance-files -p 35147

You can then access the file system of your instance at the specified location on the local machine.
