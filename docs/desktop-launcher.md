# Desktop launcher (alpha.4 and alpha.5)

The separate `findrail-desktop` executable starts the local application and
opens its UI. It uses the standard platform data directory unless `--data-dir`
is provided. The listener uses a dynamically allocated loopback port. Ordinary
`start`, `serve`, and `demo` retain their existing foreground behavior and port
defaults.

Desktop ownership is acquired before SQLite is opened. An OS-held exclusive
lock on `.desktop.lock` serializes processes for the canonical data directory;
on Unix it is an advisory file lock and on Windows a byte-range file lock. A
bounded `.desktop-instance.json` record is published only after the listener is
bound. A second launch accepts the record only when its loopback URL answers
the Findrail identity route with the same canonical data directory and private
per-instance nonce. It then opens the verified URL. A malformed record is an
error, not permission to open an arbitrary URL or claim a port. Process IDs are
not used to establish ownership.

The UI's **Quit Findrail** action is available only in this desktop composition
and uses the same origin, host, Fetch Metadata, bounded body, and in-memory
management token checks as other local mutations. Shutdown stops HTTP and sync,
drains/cancels management jobs, closes SQLite, removes only this owner's record,
and releases its OS lock. Closing a browser tab does not stop the application.
Ctrl+C also stops the foreground desktop process. `serve` and `demo` do not
expose the desktop identity or quit routes.

## Native integration handoff to packaging

Entrypoint sources are in `packaging/desktop/`: Linux menu template and
per-user install/uninstall scripts; macOS `Info.plist` and `Findrail` launcher;
Windows per-user shortcut install/uninstall PowerShell scripts.

Archive contract for packaging: place `findrail-desktop` (or
`findrail-desktop.exe`) at archive root beside the normal `findrail` binary and
normal extraction resources. For macOS app layout, place the launcher at
`Findrail.app/Contents/MacOS/Findrail` and the desktop executable beside it as
`Findrail.app/Contents/MacOS/findrail-desktop`; put `Info.plist` at
`Findrail.app/Contents/Info.plist`. Integrations are per-user and must not
remove the data directory. The packaging workflow includes these resources
and builds the Windows launcher with GUI-subsystem linking.

Manual GUI acceptance remains necessary on Linux, macOS, and Windows: install
from an extracted candidate, launch from the OS menu/shortcut, launch it a
second time, close only the tab and confirm the server remains, Quit from the
UI, then uninstall and confirm the data directory still exists. Cross-compiling
does not count as native GUI or ARM runtime validation.
