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

Packages are available for a number of Linux distributions, either in their main repository or through third party repositories.

````{tabs}

```{group-tab} Debian and Ubuntu
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
```

```{group-tab} Gentoo
Incus and all of its dependencies are available in Gentoo's main repository as [`app-containers/incus`](https://packages.gentoo.org/packages/app-containers/incus).

Install Incus with:

    emerge -av app-containers/incus

Note: Installing LTS vs. feature-release will be explained later, when Incus upstream and Gentoo's repository has those releases available.

There will be two newly created groups associated to Incus:
`incus` to allow basic user access (launch containers), and `incus-admin` for `incus admin` controls. Add your regular users to either, or both, depending on your setup and use cases.

After installation, you may want to configure Incus. This is optional though, as the defaults should also just work.

- **`openrc`**: Edit `/etc/conf.d/incus`
- **`systemd`**: `systemctl edit --full incus.service`

Set up `/etc/subuid` and `/etc/subgid`:

    echo "root:1000000:1000000000" | tee -a /etc/subuid /etc/subgid

For more information: {ref}`Idmaps for user namespace <userns-idmap>`

Start the daemon:

- **`openrc`**: `rc-service incus start`
- **`systemd`**: `systemctl start incus`

Initialize Incus, needs to be done once after a new installation:

    incus admin init

or

    incus admin init --minimal

which will just use default settings without prompting for choices. See {ref}`initialize` for more information.

Log in to your user and start using Incus through `incus` command.
```

```{group-tab} NixOS
Incus and its dependencies are packaged in NixOS and are configurable through NixOS options. See [`virtualisation.incus`](https://search.nixos.org/options?query=virtualisation.incus) for a complete set of available options.

The service can be enabled and started by adding the following to your NixOS configuration.

    virtualisation.incus.enable = true;

Incus initialization can be done manually using `incus admin init`, or through the preseed option in your NixOS configuration. See the NixOS documentation for an example preseed.

    virtualisation.incus.preseed = {};

Finally, you can add users to the `incus-admin` group to provide non-root access to the Incus socket. In your NixOS configuration:

    users.users.YOUR_USERNAME.extraGroups = ["incus-admin"];

For any NixOS specific issues, please [file an issue](https://github.com/NixOS/nixpkgs/issues/new/choose) in the package repository.
```

````

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

You can also find native builds of the Incus client on [GitHub](https://github.com/lxc/incus/actions):

- Incus client for Linux: [`bin.linux.incus.aarch64`](https://github.com/lxc/incus/releases/latest/download/bin.linux.incus.aarch64), [`bin.linux.incus.x86_64`](https://github.com/lxc/incus/releases/latest/download/bin.linux.incus.x86_64)
- Incus client for Windows: [`bin.windows.incus.aarch64.exe`](https://github.com/lxc/incus/releases/latest/download/bin.windows.incus.aarch64.exe), [`bin.windows.incus.x86_64.exe`](https://github.com/lxc/incus/releases/latest/download/bin.windows.incus.x86_64.exe)
- Incus client for macOS: [`bin.macos.incus.aarch64`](https://github.com/lxc/incus/releases/latest/download/bin.macos.incus.aarch64), [`bin.macos.incus.x86_64`](https://github.com/lxc/incus/releases/latest/download/bin.macos.incus.x86_64)

(installing_from_source)=
## Install Incus from source

Follow these instructions if you want to build and install Incus from the source code.

We recommend having the latest versions of `liblxc` (>= 4.0.0 required)
available for Incus development. Additionally, Incus requires a modern Golang (see {ref}`requirements-go`)
version to work.

````{tabs}

```{group-tab} Debian and Ubuntu
Install the build and required runtime dependencies with:

    sudo apt update
    sudo apt install acl attr autoconf automake dnsmasq-base git golang-go libacl1-dev libcap-dev liblxc1 liblxc-dev libsqlite3-dev libtool libudev-dev liblz4-dev libuv1-dev make pkg-config rsync squashfs-tools tar tcl xz-utils ebtables

There are a few storage drivers for Incus besides the default `dir` driver.
Installing these tools adds a bit to initramfs and may slow down your
host boot, but are needed if you'd like to use a particular driver:

    sudo apt install btrfs-progs
    sudo apt install ceph-common
    sudo apt install lvm2 thin-provisioning-tools
    sudo apt install zfsutils-linux

To run the test suite, you'll also need:

    sudo apt install busybox-static curl gettext jq sqlite3 socat bind9-dnsutils

****NOTE:**** If you use the `liblxc-dev` package and get compile time errors when building the `go-lxc` module,
ensure that the value for `LXC_DEVEL` is `0` for your `liblxc` build. To check that, look at `/usr/include/lxc/version.h`.
If the `LXC_DEVEL` value is `1`, replace it with `0` to work around the problem. It's a packaging bug, and
we are aware of it for Ubuntu 22.04/22.10. Ubuntu 23.04/23.10 does not have this problem.

```


```{group-tab} Alpine Linux
You can get the development resources required to build Incus on your Alpine Linux via the following command:

    apk add acl-dev autoconf automake eudev-dev gettext-dev go intltool libcap-dev libtool libuv-dev linux-headers lz4-dev tcl-dev sqlite-dev lxc-dev make xz

To take advantage of all the necessary features of Incus, you must install additional packages.
You can reference the list of packages you need to use specific functions from [LXD package definition in Alpine Linux repository](https://gitlab.alpinelinux.org/alpine/infra/aports/-/blob/master/community/lxd/APKBUILD). <!-- wokeignore:rule=master -->
Also you can find the package you need with the binary name from [Alpine Linux packages contents filter](https://pkgs.alpinelinux.org/contents).

Install the main dependencies:

    apk add acl attr ca-certificates cgmanager dbus dnsmasq lxc libintl iproute2 iptables netcat-openbsd rsync squashfs-tools shadow-uidmap tar xz

Install the extra dependencies for running virtual machines:

    apk add qemu-system-x86_64 qemu-chardev-spice qemu-hw-usb-redirect qemu-hw-display-virtio-vga qemu-img qemu-ui-spice-core ovmf sgdisk util-linux-misc virtiofsd

After preparing the source from a release tarball or git repository, you need follow the below steps to avoid known issues during build time:


****NOTE:**** Some build errors may occur if `/usr/local/include` doesn't exist on the system.
Also, due to a [`gettext` issue](https://github.com/gosexy/gettext/issues/1), you may need to set those additional environment variables:

    export CGO_LDFLAGS="$CGO_LDFLAGS -L/usr/lib -lintl"
    export CGO_CPPFLAGS="-I/usr/include"
```

````

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
sudo -E PATH=${PATH} LD_LIBRARY_PATH=${LD_LIBRARY_PATH} $(go env GOPATH)/bin/incusd --group sudo
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
This update happens automatically when the daemon starts up after an Incus upgrade.
A backup of the database before the update is stored in the same location as the active database (at `/var/lib/incus/database`).

```{important}
After a schema update, older versions of Incus might regard the database as invalid.
That means that downgrading Incus might render your Incus installation unusable.

In that case, if you need to downgrade, restore the database backup before starting the downgrade.
```
