#!/bin/sh
set -eu
DIR=${XDG_DATA_HOME:-"$HOME/.local/share"}/applications
if [ "${1-}" = --destination ] && [ "$#" -eq 2 ]; then DIR=$2
elif [ "$#" -ne 0 ]; then echo 'Usage: uninstall-user.sh [--destination applications-directory]' >&2; exit 2; fi
DEST=$DIR/findrail.desktop
if [ -L "$DEST" ]; then echo 'Refusing to remove a symlink desktop entry.' >&2; exit 1; fi
if [ -e "$DEST" ]; then
  if [ ! -f "$DEST" ] || { ! grep -q '^X-Findrail-Managed=true$' "$DEST" && ! { grep -qx 'Name=Findrail' "$DEST" && grep -q '^Exec=/usr/bin/env ".*/findrail-desktop"$' "$DEST" && grep -q '^TryExec=.*/findrail-desktop$' "$DEST"; }; }; then
    echo 'Refusing to remove an unowned desktop entry (legacy alpha.4 entries require manual review).' >&2; exit 1
  fi
  rm -- "$DEST"
fi
printf 'Removed Findrail menu entry. User data was not changed.\n'
