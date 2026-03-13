#!/bin/sh
set -eu

if [ ! -e "rc.d" ] || [ ! -e "incus-agent" ]; then
    echo "This script must be run from within the 9p mount"
    exit 1
fi

# Install the service.
mkdir -p /usr/local/etc/rc.d
mkdir -p /usr/local/libexec
cp rc.d/incus-agent /usr/local/etc/rc.d/
cp incus-agent-setup /usr/local/libexec/
sysrc incus_agent_enable=YES

echo ""
echo "Incus agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: service incus-agent start"
