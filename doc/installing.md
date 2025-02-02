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

```{group-tab} Alpine
Incus and all of its dependencies are available in Alpine Linux's edge main and community repository as `incus`.

Uncomment the edge main and community repositories in `/etc/apk/repositories` and run:

    apk update

Install Incus with:

    apk add incus incus-client

If running virtual machines, also do:

    apk add incus-vm

Then enable and start the service:

    rc-update add incusd
    rc-service incusd start

Please report packaging issues [here](https://gitlab.alpinelinux.org/alpine/aports/-/issues).
```

```{group-tab} Arch Linux
Incus and all of its dependencies are available in Arch Linux's main repository as `incus`.

Install Incus with:

    pacman -S incus

See also [the Incus documentation page at Arch Linux](https://wiki.archlinux.org/title/Incus) for more details about the installation, configuration, use and troubleshooting.

Please report packaging issues [here](https://gitlab.archlinux.org/archlinux/packaging/packages/incus).
```

```{group-tab} Chimera Linux
Incus and its dependencies are available in Chimera Linux's `user` repository as `incus`. Enable the user repository:

    apk add chimera-repo-user
    apk update

Then add the `incus` package; this will install other dependencies including `incus-client`. Enable the service.

    apk add incus
    dinitctl enable incus

If running virtual machines, also add the EDK2 firmware. Note that Chimera Linux does not provide complete support for Secure Boot, so virtual machines must be launched with this feature disabled per the example.

    apk add qemu-edk2-firmware
    dinitctl restart incus
    # example, launch virtual machine with secureboot disabled:
    # incus launch images:debian/12 --vm -c security.secureboot=false

Please report packaging issues [here](https://github.com/chimera-linux/cports/issues).
```

```{group-tab} Debian
There are three options currently available to Debian users.

1. Native `incus` package

    A native `incus` package is currently available in the Debian testing and unstable repositories.
    This package will be featured in the upcoming Debian 13 (`trixie`) release.

    On such systems, just running `apt install incus` will get Incus installed.
    To run virtual machines, also run `apt install qemu-system`.
    If migrating from LXD, also run `apt install incus-tools` to get the `lxd-to-incus` command.

1. Native `incus` backported package

   A native `incus` backported package is currently available for Debian 12 (`bookworm`) users.

   On such systems, just running `apt install incus/bookworm-backports` will get Incus installed.
   To run virtual machines, also run `apt install qemu-system`.
   If migrating from LXD, also run `apt install incus-tools` to get the `lxd-to-incus` command.

   ****NOTE:**** Users of backported packages should not file bugs in the Debian Bug Tracker, instead please reach out [through our forum](https://discuss.linuxcontainers.org) or directly to the Debian packager.

1. Zabbly package repository

    [Zabbly](https://zabbly.com) provides up to date and supported Incus packages for Debian stable releases (11 and 12).
    Those packages contain everything needed to use all Incus features.

    Up to date installation instructions may be found here: [`https://github.com/zabbly/incus`](https://github.com/zabbly/incus)
```

```{group-tab} Docker
Docker/Podman images of Incus, based on the Zabbly package repository, are available with instructions here: [`ghcr.io/cmspam/incus-docker`](https://ghcr.io/cmspam/incus-docker)
```

```{group-tab} Fedora
Incus and all of its dependencies are available in Fedora.

Install Incus with:

    dnf install incus

Please report packaging issues [here](https://bugzilla.redhat.com/).
```

```{group-tab} Gentoo
Incus and all of its dependencies are available in Gentoo's main repository as [`app-containers/incus`](https://packages.gentoo.org/packages/app-containers/incus).

Install Incus with:

    emerge -av app-containers/incus

To run virtual machines, also run:

    emerge -av app-emulation/qemu

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

Continue in the [Gentoo Wiki](https://wiki.gentoo.org/wiki/Incus).
```

```{group-tab} NixOS
Incus and its dependencies are packaged in NixOS and are configurable through NixOS options. See [`virtualisation.incus`](https://search.nixos.org/options?query=virtualisation.incus) for a complete set of available options.

The service can be enabled and started by adding the following to your NixOS configuration.

    virtualisation.incus.enable = true;

Incus initialization can be done manually using `incus admin init`, or through the preseed option in your NixOS configuration. See the NixOS documentation for an example preseed.

    virtualisation.incus.preseed = {};

Finally, you can add users to the `incus-admin` group to provide non-root access to the Incus socket. In your NixOS configuration:

    users.users.YOUR_USERNAME.extraGroups = ["incus-admin"];

Instead of giving the users a full Incus daemon access, you can add users to the `incus` group, which will only grant access to the Incus user socket. In your NixOS configuration:

    users.users.YOUR_USERNAME.extraGroups = ["incus"];

For any NixOS specific issues, please [file an issue](https://github.com/NixOS/nixpkgs/issues/new/choose) in the package repository.
```

```{group-tab} openSUSE
Incus and its dependencies are packaged in both openSUSE Tumbleweed and openSUSE Leap 15.6 and later (this is available through openSUSE Backports, so you can also install the same packages through PackageHub for SUSE Linux Enterprise Server 15 SP6 and later, though no support is provided by SUSE for said packages).

Install Incus with:

    zypper in incus

If migrating from LXD, please also install `incus-tools` for `lxd-to-incus`.

The default setup should work fine for most users, but if you intend to run many containers on your system you may wish to apply some custom `sysctl` settings [as suggested in the production deployments guide](./reference/server_settings.md).

Please report packaging issues [here](https://bugzilla.opensuse.org/).
Make sure to mark the bug as being in the "Containers" component, to make sure the right package maintainers see the bug.
```

```{group-tab} Rocky Linux
RPM packages and their dependencies are not yet available from the Extra Packages for Enterprise Linux (EPEL) repository, but via the [`neil/incus`](https://copr.fedorainfracloud.org/coprs/neil/incus/) Community Project (COPR) repository for Rocky Linux 9.

Ensure that the EPEL repository is installed for package dependencies and then install the COPR repository:

    dnf -y install epel-release
    dnf copr enable neil/incus

Ensure that the `CodeReady Builder` (`CRB`) is available for other package dependencies:

    dnf config-manager --enable crb

Then install Incus and optionally, Incus tools:

    dnf install incus incus-tools

Note that this is not an official project of Incus nor Rocky Linux.
Please report packaging issues [here](https://github.com/NeilHanlon/incus-rpm/issues).
```

```{group-tab} Ubuntu
There are two options currently available to Ubuntu users.

1. Native `incus` package

    A native `incus` package is currently available in Ubuntu 24.04 LTS and later.
    On such systems, just running `apt install incus` will get Incus installed.
    To run virtual machines, also run `apt install qemu-system`.
    If migrating from LXD, also run `apt install incus-tools` to get the `lxd-to-incus` command.

1. Zabbly package repository

    [Zabbly](https://zabbly.com) provides up to date and supported Incus packages for Ubuntu LTS releases (20.04 and 22.04).
    Those packages contain everything needed to use all Incus features.

    Up to date installation instructions may be found here: [`https://github.com/zabbly/incus`](https://github.com/zabbly/incus)
```

```{group-tab} Void Linux
Incus and all of its dependencies are available in Void Linux's repository as `incus`.

Install Incus with:

    xbps-install incus incus-client

Then enable and start the services with:

    ln -s /etc/sv/incus /var/service
    ln -s /etc/sv/incus-user /var/service
    sv up incus
    sv up incus-user

Please report packaging issues [here](https://github.com/void-linux/void-packages/issues).
```

````

### Other operating systems

```{important}
The builds for other operating systems include only the client, not the server.
```

````{tabs}

```{group-tab} macOS

**Homebrew**

Incus publishes builds of the Incus client for macOS through [Homebrew](https://brew.sh/).

To install the feature branch of Incus, run:

    brew install incus

**Colima**

Incus is supported as a runtime on [Colima](https://github.com/abiosoft/colima).

Install Colima with:

    brew install colima

Start Colima with Incus as runtime with:

    colima start --runtime incus

For any Colima related issues, please [file an issue](https://github.com/abiosoft/colima/issues/new/choose) in the project repository.
```

```{group-tab} Windows

The Incus client on Windows is provided as a [Chocolatey](https://community.chocolatey.org/packages/incus) and [Winget](https://github.com/microsoft/winget-cli) package.
To install it using Chocolatey or Winget, follow the instructions below:

**Chocolatey**

1. Install Chocolatey by following the [installation instructions](https://docs.chocolatey.org/en-us/choco/setup).
1. Install the Incus client:

        choco install incus

**Winget**

1. Install Winget by following the [installation instructions](https://learn.microsoft.com/en-us/windows/package-manager/winget/#install-winget)
1. Install the Incus client:

        winget install LinuxContainers.Incus

```

````

You can also find native builds of the Incus client on [GitHub](https://github.com/lxc/incus/actions):

- Incus client for Linux: [`bin.linux.incus.aarch64`](https://github.com/lxc/incus/releases/latest/download/bin.linux.incus.aarch64), [`bin.linux.incus.x86_64`](https://github.com/lxc/incus/releases/latest/download/bin.linux.incus.x86_64)
- Incus client for Windows: [`bin.windows.incus.aarch64.exe`](https://github.com/lxc/incus/releases/latest/download/bin.windows.incus.aarch64.exe), [`bin.windows.incus.x86_64.exe`](https://github.com/lxc/incus/releases/latest/download/bin.windows.incus.x86_64.exe)
- Incus client for macOS: [`bin.macos.incus.aarch64`](https://github.com/lxc/incus/releases/latest/download/bin.macos.incus.aarch64), [`bin.macos.incus.x86_64`](https://github.com/lxc/incus/releases/latest/download/bin.macos.incus.x86_64)

(installing_from_source)=
## Install Incus from source

Follow these instructions if you want to build and install Incus from the source code.

We recommend having the latest versions of `liblxc` (>= 5.0.0 required)
available for Incus development. Additionally, Incus requires a modern Golang (see {ref}`requirements-go`)
version to work.

````{tabs}

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

```{group-tab} Debian and Ubuntu
Install the build and required runtime dependencies with:

    sudo apt update
    sudo apt install acl attr autoconf automake dnsmasq-base git golang-go libacl1-dev libcap-dev liblxc1 lxc-dev libsqlite3-dev libtool libudev-dev liblz4-dev libuv1-dev make pkg-config rsync squashfs-tools tar tcl xz-utils ebtables

****NOTE:**** The version of `golang-go` in your version of Debian or Ubuntu may not be sufficient to build Incus (see {ref}`requirements-go`).
In such cases, you may need to install a newer Go version [from upstream](https://go.dev/doc/install).

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

```{group-tab} OpenSUSE
You can get the development resources required to build Incus on your OpenSUSE Tumbleweed system via the following command:

    sudo zypper install autoconf automake git go libacl-devel libcap-devel liblxc1 liblxc-devel sqlite3-devel libtool libudev-devel liblz4-devel libuv-devel make pkg-config tcl

In addition, for normal operation, you'll also likely need:

    sudo zypper install dnsmasq squashfs xz rsync tar attr acl qemu qemu-img qemu-spice qemu-hw-display-virtio-gpu-pci iptables ebtables nftables

For using NVIDIA GPUs inside containers, you will need the NVIDIA container tools and LXC hooks:

    sudo zypper install libnvidia-container-tools lxc

```


````

```{note}
On ARM64 CPUs you need to install AAVMF instead of OVMF for UEFI to work with virtual machines.
In some distributions this is done through a separate package.
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
tar zxvf incus-6.0.0.tar.gz
cd incus-6.0.0
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
