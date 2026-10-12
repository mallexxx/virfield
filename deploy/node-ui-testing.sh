#!/bin/bash
# Enables unattended XCUITest on the booted macOS volume of a physical node.
#
# Usage (operator, once per boot volume; asks for the node's sudo password):
#   scp deploy/node-ui-testing.sh <user>@<node>:
#   ssh -t <user>@<node> 'bash ~/node-ui-testing.sh'
# Copy first: piping the script over stdin leaves sudo without a terminal.
#
# Virfield controls nodes as an unprivileged account, so it cannot do this
# itself. Without it testmanagerd asks for an admin password on the console
# ("Timed out while enabling automation mode") and no agent can answer.
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    echo "Usage: scp deploy/node-ui-testing.sh <user>@<node>: && ssh -t <user>@<node> 'bash ~/node-ui-testing.sh'"
    echo "Enables Developer Mode, _developer membership and password-less Automation Mode for unattended XCUITest."
    exit 0
fi

user="$(id -un)"
sudo DevToolsSecurity -enable
sudo dseditgroup -o edit -a "$user" -t user _developer
# testmanagerd enables Automation Mode per test session; by default that asks
# for a password through LocalAuthentication (macOS 12.6 and later).
if [[ -x /usr/bin/automationmodetool ]]; then
    sudo /usr/bin/automationmodetool enable-automationmode-without-authentication
    /usr/bin/automationmodetool
fi

DevToolsSecurity -status
console="$(stat -f %Su /dev/console)"
if [[ "$console" != "$user" ]]; then
    echo "Console session belongs to '$console', not '$user'."
    echo "Log in '$user' on the node's display (enable automatic login) for UI tests."
    exit 1
fi
echo "ui_testing=ready for $user on $(sw_vers -productVersion)"
