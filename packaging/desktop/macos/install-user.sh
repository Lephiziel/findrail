#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
DEST=${HOME}/Applications
if [ "${1-}" = --destination ] && [ "$#" -eq 2 ]; then DEST=$2
elif [ "$#" -ne 0 ]; then echo 'Usage: install-user.sh [--destination applications-directory]' >&2; exit 2; fi
mkdir -p "$DEST"
TARGET=$DEST/Findrail.app
if [ -L "$TARGET" ] || { [ -e "$TARGET" ] && { [ ! -d "$TARGET" ] || { [ ! -f "$TARGET/Contents/.findrail-managed" ] && ! grep -q '<string>org.findrail.desktop</string>' "$TARGET/Contents/Info.plist" 2>/dev/null; }; }; }; then
  echo 'Refusing to replace an existing unowned Findrail.app.' >&2; exit 1
fi
STAGE=$(mktemp -d "$DEST/.Findrail.app.stage.XXXXXX")
OLD=$DEST/.Findrail.app.previous.$$
cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT HUP INT TERM
cp -R "$HERE/Findrail.app/." "$STAGE/"
printf '%s\n' 'org.findrail.desktop' > "$STAGE/Contents/.findrail-managed"
if [ -e "$TARGET" ]; then mv "$TARGET" "$OLD"; fi
if ! mv "$STAGE" "$TARGET"; then
  [ ! -e "$OLD" ] || mv "$OLD" "$TARGET"
  echo 'Could not publish staged app; previous app restored.' >&2; exit 1
fi
[ ! -e "$OLD" ] || rm -rf "$OLD"
trap - EXIT HUP INT TERM
printf 'Installed Findrail.app for this user. User data was not changed.\n'
