#!/bin/sh
set -eu
BIN=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)/findrail-desktop
DEST=${XDG_DATA_HOME:-"$HOME/.local/share"}/applications
if [ "${1-}" = --destination ] && [ "$#" -eq 2 ]; then DEST=$2
elif [ "$#" -ne 0 ]; then echo 'Usage: install-user.sh [--destination applications-directory]' >&2; exit 2; fi
INSTALL_DIR=$(dirname "$BIN")
NL='
'
CR=$(printf '\r')
case "$INSTALL_DIR" in *"$NL"*|*"$CR"*|*=*) echo 'Install path cannot contain a newline or equals sign.' >&2; exit 1 ;; esac
ESCAPED_DIR=$(printf '%s' "$INSTALL_DIR" | sed -e 's/\\/\\\\\\\\/g' -e 's/["`$]/\\\\&/g' -e 's/%/%%/g')
STRING_DIR=$(printf '%s' "$INSTALL_DIR" | sed -e 's/\\/\\\\/g')
mkdir -p "$DEST"
TARGET=$DEST/findrail.desktop
if [ -L "$TARGET" ] || { [ -e "$TARGET" ] && { [ ! -f "$TARGET" ] || { ! grep -q '^X-Findrail-Managed=true$' "$TARGET" && ! { grep -qx 'Name=Findrail' "$TARGET" && grep -q '^Exec=/usr/bin/env ".*/findrail-desktop"$' "$TARGET" && grep -q '^TryExec=.*/findrail-desktop$' "$TARGET"; }; }; }; }; then
  echo 'Refusing to replace an existing unowned Findrail desktop entry.' >&2; exit 1
fi
TMP=$(mktemp "$DEST/.findrail.desktop.XXXXXX")
trap 'rm -f "$TMP"' EXIT HUP INT TERM
while IFS= read -r line; do
  case "$line" in
    Exec=*) printf 'Exec=/usr/bin/env "%s/findrail-desktop"\n' "$ESCAPED_DIR" ;;
    TryExec=*) printf 'TryExec=%s/findrail-desktop\n' "$STRING_DIR" ;;
    *) printf '%s\n' "$line" ;;
  esac
done < "$(dirname "$0")/findrail.desktop" > "$TMP"
printf 'X-Findrail-Managed=true\n' >> "$TMP"
chmod 644 "$TMP"
mv -f "$TMP" "$TARGET"
trap - EXIT HUP INT TERM
printf 'Installed user menu entry: %s\n' "$TARGET"
