#!/bin/sh

set -eu

if [ ! -e "launchd" ] || [ ! -e "incus-agent" ]; then
    echo "This script must be run from within the 9p mount"
    exit 1
fi

AGENT=org.linuxcontainers.incus.macos-agent

echo "Installing agent"

# Uninstall the previous daemon.
if launchctl print "system/$AGENT" >/dev/null 2>&1; then
    launchctl bootout system "/Library/LaunchDaemons/$AGENT.plist" || true
fi

# Install the daemon.
cp "launchd/$AGENT.plist" /Library/LaunchDaemons/
chown root:wheel "/Library/LaunchDaemons/$AGENT.plist"
cp incus-agent-setup /usr/local/bin/
chown root:wheel /usr/local/bin/incus-agent-setup

# Bootstrap it.
launchctl bootstrap system "/Library/LaunchDaemons/$AGENT.plist"

# Enable it.
launchctl enable "system/$AGENT"

echo ""
echo "Incus agent has been installed, reboot to confirm setup."
echo "To start it now, run: sudo launchctl kickstart -k system/$AGENT"
echo "Don't forget to allow full disk access to sh."
