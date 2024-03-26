#!/bin/sh
if [ ! -e "systemd" ] || [ ! -e "incus-agent" ]; then
    echo "This script must be run from within the 9p mount"
    exit 1
fi

# Find target path.
TARGET=""
for alternative in /usr/lib /lib /etc; do
    [ -w "${alternative}/systemd" ] || continue
    [ -w "${alternative}/udev" ] || continue

    TARGET="${alternative}"
    break
done

if [ "${TARGET}" = "" ]; then
    echo "This script only works on systemd systems"
    exit 1
fi

echo "Installing agent into ${TARGET}"

# Install the units.
cp udev/99-incus-agent.rules "${TARGET}/udev/rules.d/"
cp systemd/incus-agent.service "${TARGET}/systemd/system/"
cp systemd/incus-agent-setup "${TARGET}/systemd/"

# Replacing the variables.
sed -i "s#TARGET#${TARGET}#g" "${TARGET}/udev/rules.d/99-incus-agent.rules"
sed -i "s#TARGET#${TARGET}#g" "${TARGET}/systemd/system/incus-agent.service"
sed -i "s#TARGET#${TARGET}#g" "${TARGET}/systemd/incus-agent-setup"

# Make sure systemd is aware of them.
systemctl daemon-reload

# SELinux handling.
if getenforce >/dev/null 2>&1 && type semanage >/dev/null 2>&1; then
    semanage fcontext -a -t bin_t /var/run/incus_agent/incus-agent
fi

echo ""
echo "Incus agent has been installed, reboot to confirm setup."
echo "To start it now, unmount this filesystem and run: systemctl start incus-agent"
