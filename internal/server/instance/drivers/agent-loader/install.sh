#!/bin/sh
if [ ! -e "systemd" ] || [ ! -e "incus-agent" ]; then
    echo "This script must be run from within the 9p mount"
    exit 1
fi

if [ ! -e "/lib/systemd/system" ]; then
    echo "This script only works on systemd systems"
    exit 1
fi

# Install the units.
cp udev/99-incus-agent.rules /lib/udev/rules.d/
cp systemd/incus-agent.service /lib/systemd/system/
cp systemd/incus-agent-setup /lib/systemd/
systemctl daemon-reload

echo ""
echo "Incus agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: systemctl start incus-agent"
