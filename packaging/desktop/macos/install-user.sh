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
WORK=$(mktemp -d "$DEST/.Findrail.app.stage.XXXXXX")
STAGE=$WORK/new-app
OLD=$WORK/previous-app
PUBLISHED=0
mkdir "$STAGE"
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ "$PUBLISHED" -eq 0 ] && [ -e "$OLD" ]; then
    if [ -e "$TARGET" ] || [ -L "$TARGET" ] || ! mv "$OLD" "$TARGET"; then
      echo "Previous app retained for recovery in $WORK" >&2
      exit 1
    fi
  fi
  rm -rf "$WORK"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
cp -R "$HERE/Findrail.app/." "$STAGE/"
printf '%s\n' 'org.findrail.desktop' > "$STAGE/Contents/.findrail-managed"
if [ -e "$TARGET" ]; then mv "$TARGET" "$OLD"; fi
if ! mv "$STAGE" "$TARGET"; then
  echo 'Could not publish staged app; restoring the previous app.' >&2; exit 1
fi
PUBLISHED=1
printf 'Installed Findrail.app for this user. User data was not changed.\n'
