#!/bin/bash
# The upstream image's host keys ship in a public digest. Give every derived
# golden its own keys; sshd loads them on the next boot, when Virfield pins them.
set -euo pipefail
sudo -n /bin/rm -f /etc/ssh/ssh_host_*
sudo -n /usr/bin/ssh-keygen -A
test -s /etc/ssh/ssh_host_ed25519_key
