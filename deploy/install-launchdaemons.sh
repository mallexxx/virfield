#!/bin/bash
# Reviewed one-host migration: launchd system jobs run as admin, never as root.
set -euo pipefail
[[ "$(/usr/bin/id -u)" == 0 ]] || { echo 'Administrator authorization required' >&2; exit 1; }
for label in ai.virfield.virfieldd ai.virfield.lume; do
  /usr/bin/plutil -lint "/Users/admin/.virfield-v2/launchd/$label.plist"
  /usr/bin/install -o root -g wheel -m 644 "/Users/admin/.virfield-v2/launchd/$label.plist" "/Library/LaunchDaemons/$label.plist"
done
for label in ai.virfield.virfieldd ai.virfield.lume; do
  /bin/launchctl bootout "gui/501/$label"
  /bin/mv "/Users/admin/Library/LaunchAgents/$label.plist" "/Users/admin/.virfield-v2/migration-backup/$label.agent.plist"
done
/bin/launchctl bootstrap system /Library/LaunchDaemons/ai.virfield.lume.plist
/bin/launchctl bootstrap system /Library/LaunchDaemons/ai.virfield.virfieldd.plist
