(installing)=
# How to install Incus

The easiest way to install Incus is to {ref}`install one of the available packages <installing-from-package>`, but you can also {ref}`install Incus from the sources <installing_from_source>`.

After installing Incus, make sure you have an `incus-admin` group on your system.
Users in this group can interact with Incus.
See {ref}`installing-manage-access` for instructions.

## Choose your release

% Include content from [support.md](support.md)
```{include} support.md
    :start-after: <!-- Include start release -->
    :end-before: <!-- Include end release -->
```

LTS releases are recommended for production environments, because they benefit from regular bugfix and security updates.
However, there are no new features added to an LTS release, nor any kind of behavioral change.

To get all the latest features and monthly updates to Incus, use the feature release branch instead.

(installing-from-package)=
## Install Incus from a package

The Incus daemon only works on Linux.
The client tool ([`incus`](incus.md)) is available on most platforms.

### Linux

The easiest way to install Incus on Linux is to install the {ref}`installing-zabbly-package`, which is available for both Debian and Ubuntu.

(installing-zabbly-package)=
#### Debian and Ubuntu package from Zabbly
Currently the easiest way to install Incus is to use the Debian or Ubuntu packages provided by [Zabbly](https://zabbly.com).
There are two repositories available, one for the current stable release and one for daily (untested) builds.

Installation instructions may be found here: [`https://github.com/zabbly/incus`](https://github.com/zabbly/incus)

If you prefer a different installation method, see {ref}`installing`.

1. Allow your user to control Incus

   Access to Incus in the packages above is controlled through two groups:

   - `incus` allows basic user access, no configuration and all actions restricted to a per-user project.
   - `incus-admin` allows full control over Incus.

   To control Incus without having to run all commands as root, you can add yourself to the `incus-admin` group:

       sudo adduser YOUR-USERNAME incus-admin
       newgrp incus-admin

   The `newgrp` step is needed in any terminal that interacts with Incus until you restart your user session.

1. Initialize Incus with:

       incus admin init --minimal

   This will create a minimal setup with default options.
   If you want to tune the initialization options, see {ref}`initialize` for more information.

### Other operating systems

```{important}
The builds for other operating systems include only the client, not the server.
```

````{tabs}

```{group-tab} macOS

Incus publishes builds of the Incus client for macOS through [Homebrew](https://brew.sh/).

To install the feature branch of Incus, run:

    brew install incus
```

```{group-tab} Windows

The Incus client on Windows is provided as a [Chocolatey](https://community.chocolatey.org/packages/lxc) package.
To install it:

1. Install Chocolatey by following the [installation instructions](https://docs.chocolatey.org/en-us/choco/setup).
1. Install the Incus client:

        choco install incus
```

````

You can also find native builds of the Incus client on [GitHub](https://github.com/lxc/incus/actions).
To download a specific build:

1. Make sure that you are logged into your GitHub account.
1. Filter for the branch or tag that you are interested in (for example, the latest release tag or `main`).
1. Select the latest build and download the suitable artifact.

(installing_from_source)=
## Install Incus from source

Follow these instructions if you want to build and install Incus from the source code.

We recommend having the latest versions of `liblxc` (>= 4.0.0 required)
available for Incus development. Additionally, Incus requires Golang 1.18 or
later to work. On Ubuntu, you can get those with:

```bash
sudo apt update
sudo apt install acl attr autoconf automake dnsmasq-base git golang libacl1-dev libcap-dev liblxc1 liblxc-dev libsqlite3-dev libtool libudev-dev liblz4-dev libuv1-dev make pkg-config rsync squashfs-tools tar tcl xz-utils ebtables
```

There are a few storage drivers for Incus besides the default `dir` driver.
Installing these tools adds a bit to initramfs and may slow down your
host boot, but are needed if you'd like to use a particular driver:

```bash
sudo apt install lvm2 thin-provisioning-tools
sudo apt install btrfs-progs
```

To run the test suite, you'll also need:

```bash
sudo apt install busybox-static curl gettext jq sqlite3 socat bind9-dnsutils
```

### From source: Build the latest version

These instructions for building from source are suitable for individual developers who want to build the latest version
of Incus, or build a specific release of Incus which may not be offered by their Linux distribution. Source builds for
integration into Linux distributions are not covered here and may be covered in detail in a separate document in the
future.

```bash
git clone https://github.com/lxc/incus
cd incus
```

This will download the current development tree of Incus and place you in the source tree.
Then proceed to the instructions below to actually build and install Incus.

### From source: Build a release

The Incus release tarballs bundle a complete dependency tree as well as a
local copy of `libraft` and `libcowsql` for Incus' database setup.

```bash
tar zxvf incus-0.1.tar.gz
cd incus-0.1
```

This will unpack the release tarball and place you inside of the source tree.
Then proceed to the instructions below to actually build and install Incus.

### Start the build

The actual building is done by two separate invocations of the Makefile: `make deps` -- which builds libraries required
by Incus -- and `make`, which builds Incus itself. At the end of `make deps`, a message will be displayed which will specify environment variables that should be set prior to invoking `make`. As new versions of Incus are released, these environment
variable settings may change, so be sure to use the ones displayed at the end of the `make deps` process, as the ones
below (shown for example purposes) may not exactly match what your version of Incus requires:

We recommend having at least 2GiB of RAM to allow the build to complete.

```{terminal}
:input: make deps

...
make[1]: Leaving directory '/root/go/deps/cowsql'
# environment

Please set the following in your environment (possibly ~/.bashrc)
#  export CGO_CFLAGS="${CGO_CFLAGS} -I$(go env GOPATH)/deps/cowsql/include/ -I$(go env GOPATH)/deps/raft/include/"
#  export CGO_LDFLAGS="${CGO_LDFLAGS} -L$(go env GOPATH)/deps/cowsql/.libs/ -L$(go env GOPATH)/deps/raft/.libs/"
#  export LD_LIBRARY_PATH="$(go env GOPATH)/deps/cowsql/.libs/:$(go env GOPATH)/deps/raft/.libs/:${LD_LIBRARY_PATH}"
#  export CGO_LDFLAGS_ALLOW="(-Wl,-wrap,pthread_create)|(-Wl,-z,now)"
:input: make
```

### From source: Install

Once the build completes, you simply keep the source tree, add the directory referenced by `$(go env GOPATH)/bin` to
your shell path, and set the `LD_LIBRARY_PATH` variable printed by `make deps` to your environment. This might look
something like this for a `~/.bashrc` file:

```bash
export PATH="${PATH}:$(go env GOPATH)/bin"
export LD_LIBRARY_PATH="$(go env GOPATH)/deps/cowsql/.libs/:$(go env GOPATH)/deps/raft/.libs/:${LD_LIBRARY_PATH}"
```

Now, the `incusd` and `incus` binaries will be available to you and can be used to set up Incus. The binaries will automatically find and use the dependencies built in `$(go env GOPATH)/deps` thanks to the `LD_LIBRARY_PATH` environment variable.

### Machine setup

You'll need sub{u,g}ids for root, so that Incus can create the unprivileged containers:

```bash
echo "root:1000000:1000000000" | sudo tee -a /etc/subuid /etc/subgid
```

Now you can run the daemon (the `--group sudo` bit allows everyone in the `sudo`
group to talk to Incus; you can create your own group if you want):

```bash
sudo -E PATH=${PATH} LD_LIBRARY_PATH=${LD_LIBRARY_PATH} $(go env GOPATH)/bin/incus --group sudo
```

```{note}
If `newuidmap/newgidmap` tools are present on your system and `/etc/subuid`, `etc/subgid` exist, they must be configured to allow the root user a contiguous range of at least 10M UID/GID.
```

(installing-manage-access)=
## Manage access to Incus

Access control for Incus is based on group membership.
The root user and all members of the `incus-admin` group can interact with the local daemon.
See {ref}`security-daemon-access` for more information.

If the `incus-admin` group is missing on your system, create it and restart the Incus daemon.
You can then add trusted users to the group.
Anyone added to this group will have full control over Incus.

Because group membership is normally only applied at login, you might need to either re-open your user session or use the `newgrp incus-admin` command in the shell you're using to talk to Incus.

````{important}
% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
````

(installing-upgrade)=
## Upgrade Incus

After upgrading Incus to a newer version, Incus might need to update its database to a new schema.
This update happens automatically when the daemon starts up after a Incus upgrade.
A backup of the database before the update is stored in the same location as the active database (at `/var/lib/incus/database`).

```{important}
After a schema update, older versions of Incus might regard the database as invalid.
That means that downgrading Incus might render your Incus installation unusable.

In that case, if you need to downgrade, restore the database backup before starting the downgrade.
```
