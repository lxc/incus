(run-commands)=
# How to run commands in an instance

Incus allows to run commands inside an instance using the Incus client, without needing to access the instance through the network.

For containers, this always works and is handled directly by Incus.
For virtual machines, the `incus-agent` process must be running inside of the virtual machine for this to work.

To run commands inside your instance, use the [`incus exec`](incus_exec.md) command.
By running a shell command (for example, `/bin/bash`), you can get shell access to your instance.

## Run commands inside your instance

To run a single command from the terminal of the host machine, use the [`incus exec`](incus_exec.md) command:

    incus exec <instance_name> -- <command>

For example, enter the following command to update the package list on your container:

    incus exec ubuntu-container -- apt-get update

### Execution mode

Incus can execute commands either interactively or non-interactively.

In interactive mode, a pseudo-terminal device (PTS) is used to handle input (stdin) and output (stdout, stderr).
This mode is automatically selected by the CLI if connected to a terminal emulator (and not run from a script).
To force interactive mode, add either `--force-interactive` or `--mode interactive` to the command.

In non-interactive mode, pipes are allocated instead (one for each of stdin, stdout and stderr).
This method allows running a command and properly getting separate stdin, stdout and stderr as required by many scripts.
To force non-interactive mode, add either `--force-noninteractive` or `--mode non-interactive` to the command.

### User, groups and working directory

Incus has a policy not to read data from within the instances or trust anything that can be found in the instance.
Therefore, Incus does not parse files like `/etc/passwd`, `/etc/group` or `/etc/nsswitch.conf` to handle user and group resolution.

As a result, Incus doesn't know the home directory for the user or the supplementary groups the user is in.

By default, Incus runs commands as `root` (UID 0) with the default group (GID 0) and the working directory set to `/root`.
You can override the user, group and working directory by specifying absolute values through the following flags:

- `--user` - the user ID for running the command
- `--group` - the group ID for running the command
- `--cwd` - the directory in which the command should run

### Environment

You can pass environment variables to an exec session in the following two ways:

Set environment variables as instance options
: To set the `ENVVAR` environment variable to `VALUE` in the instance, set the `environment.ENVVAR` instance option (see {config:option}`instance-miscellaneous:environment.*`):

      incus config set <instance_name> environment.ENVVAR=VALUE

Pass environment variables to the exec command
: To pass an environment variable to the exec command, use the `--env` flag.
  For example:

      incus exec <instance_name> --env ENVVAR=VALUE -- <command>

In addition, Incus sets the following default values (unless they are passed in one of the ways described above):

```{list-table}
   :header-rows: 1

* - Variable name
  - Condition
  - Value
* - `PATH`
  - \-
  - Concatenation of:
    - `/usr/local/sbin`
    - `/usr/local/bin`
    - `/usr/sbin`
    - `/usr/bin`
    - `/sbin`
    - `/bin`
    - `/snap` (if applicable)
    - `/etc/NIXOS` (if applicable)
* - `LANG`
  - \-
  - `C.UTF-8`
* - `HOME`
  - running as root (UID 0)
  - `/root`
* - `USER`
  - running as root (UID 0)
  - `root`
```

## Get shell access to your instance

If you want to run commands directly in your instance, run a shell command inside it.
For example, enter the following command (assuming that the `/bin/bash` command exists in your instance):

    incus exec <instance_name> -- /bin/bash

By default, you are logged in as the `root` user.
If you want to log in as a different user, enter the following command:

    incus exec <instance_name> -- su --login <user_name>

```{note}
Depending on the operating system that you run in your instance, you might need to create a user first.
```

To exit the instance shell, enter `exit` or press `Ctrl`+`d`.
