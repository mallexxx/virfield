#!/bin/bash
# Cirrus Xcode images install Xcode as /Applications/Xcode_<version>.app.
# The shared recipe and qualification use /Applications/Xcode.app, so adopt
# the single installed Xcode under that name and select it.
set -euo pipefail
if [[ ! -d /Applications/Xcode.app ]]; then
  shopt -s nullglob
  installed=(/Applications/Xcode_*.app)
  (( ${#installed[@]} == 1 )) || { echo "expected one upstream Xcode, found ${#installed[@]}"; exit 65; }
  sudo -n /bin/mv "${installed[0]}" /Applications/Xcode.app
fi
sudo -n /usr/bin/xcode-select -s /Applications/Xcode.app
/usr/bin/xcodebuild -version
