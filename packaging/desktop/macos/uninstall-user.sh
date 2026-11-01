#!/bin/sh
set -eu
DEST=$HOME/Applications
if [ "${1-}" = --destination ] && [ "$#" -eq 2 ]; then DEST=$2
elif [ "$#" -ne 0 ]; then echo 'Usage: uninstall-user.sh [--destination applications-directory]' >&2; exit 2; fi
TARGET=$DEST/Findrail.app
if [ -L "$TARGET" ]; then echo 'Refusing to remove a symlink app.' >&2; exit 1; fi
if [ -e "$TARGET" ]; then
  if [ ! -d "$TARGET" ] || { [ ! -f "$TARGET/Contents/.findrail-managed" ] && ! grep -q '<string>org.findrail.desktop</string>' "$TARGET/Contents/Info.plist" 2>/dev/null; }; then
    echo 'Refusing to remove an unowned app resource.' >&2; exit 1
  fi
  rm -rf "$TARGET"
fi
printf 'Removed Findrail.app. User data was not changed.\n'
