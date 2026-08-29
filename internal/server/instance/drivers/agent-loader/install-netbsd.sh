#!/bin/sh
set -eu

if [ ! -e "rc.d" ] || [ ! -e "incus-agent" ]; then
    echo "This script must be run from within the 9p mount"
    exit 1
fi

# Install the service.
mkdir -p /etc/rc.d
mkdir -p /usr/libexec
cp rc.d/incus-agent /etc/rc.d/
cp incus-agent-setup /usr/libexec/
sed -iE "/^incus_agent[=\[:space:]]/d" /etc/rc.conf
echo incus_agent=YES >> /etc/rc.conf

echo ""
echo "Incus agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: service incus-agent start"
