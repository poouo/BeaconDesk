#!/usr/bin/env sh
set -eu

SCRIPT_URL="${BEACONDESK_INSTALLER_URL:-https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh}"

if [ -f "$(dirname "$0")/install.sh" ]; then
  exec sh "$(dirname "$0")/install.sh" uninstall
fi

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$SCRIPT_URL" | sh -s -- uninstall
elif command -v wget >/dev/null 2>&1; then
  wget -qO- "$SCRIPT_URL" | sh -s -- uninstall
else
  echo "curl or wget is required." >&2
  exit 1
fi

