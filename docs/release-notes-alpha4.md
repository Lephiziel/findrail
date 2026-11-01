# Findrail 0.1.0-alpha.4

Alpha.4 connects local search to an everyday workflow: launch Findrail, find a
passage, reopen the original file, and return to a saved collection after a
restart. The app still runs locally with its own SQLite index and embedded web
interface. Go, Python and a database server are not runtime requirements.

## Highlights

- A separate **desktop launcher**, with per-user Linux menu installation, a
  macOS app bundle and Windows Start-menu shortcut resources in the five app
  archives. A second launch reopens the verified existing instance for the
  same data directory. Closing a tab keeps the app running; **Quit Findrail**
  stops it. The CLI's foreground `start`, `serve` and `demo` remain available.
- **Open original / open containing folder**, explicitly enabled with
  `start --enable-originals` (or `findrail-desktop --enable-originals`).
  Actions apply to eligible, verified local documents from selected folders.
  Preview shows the indexed snapshot; the external app opens the current file.
  A PDF viewer may open at its default page. DOCX has no page-number claim.
- **Named evidence collections** in writable `start` and desktop sessions.
  Save exact passages, notes, names and ordering, then load them from the same
  data directory after restarting. Saving is explicit. Historical passages
  retain their original text; source removal or committed document pruning
  removes attached entries. Read-only `serve` / `demo` keep temporary in-tab
  collections, and portable source export does not include collections.
- Strengthened package and upgrade verification: desktop resources, executable
  modes, safe extraction, single-instance lifecycle, collection restoration,
  schema-6 upgrade and stopped-backup restoration with the published alpha.3
  binary. Previously published alpha.3 assets and tag remain unchanged.

Existing keyword search, Advanced queries, DOCX body text, PDF evidence, folder
refresh, public GitHub file snapshots, portable indexed-text snapshots, read-only
stdio MCP and the optional read-only VS Code client remain available.

## Install and first run

Download the archive for your OS/CPU and the matching `SHA256SUMS.txt` from this
release. Verify the archive checksum, extract it, and read `INSTALL.txt`.
The archive includes the normal CLI and the separate desktop launcher; the VSIX
is an optional separate client, version `0.1.0`, requiring server/client API v1.

For a terminal session, run `./findrail start` on Linux/macOS or
`.\findrail.exe start` in Windows PowerShell, then add a folder in **Sources**.
For OS-menu installation, run the per-user script under `launcher/linux/`,
`launcher/macos/` or `launcher/windows/`. macOS includes `Findrail.app`.
Desktop sessions use a dynamic loopback port; open the actual URL provided by
the app, rather than assuming the CLI's default port.

Native opening is opt-in. To try it in a separate data directory:

```bash
./findrail-desktop --data-dir ./findrail-alpha4-test --enable-originals
```

On macOS use the executable inside `Findrail.app/Contents/MacOS/`; on Windows
use `findrail-desktop.exe`. When reusing an existing desktop instance, the
first process's configuration remains active; stop it before changing flags.

## Upgrade and rollback

**Alpha.4 uses schema 7. Published alpha.3 uses schema 6.** Stop all processes
that use the index, then back up the complete data directory, including SQLite
WAL/SHM files when present, before the new binary opens it. Migrations 1–7 run
forward; there is no reverse migration. Alpha.3 cannot read a schema-7 index.
Rollback restores the whole stopped, pre-upgrade backup to a separate location
and uses the corresponding older binary. Use a fresh data directory for a first
test if you do not want to migrate your existing index.

Uninstalling menu/shortcut/app resources preserves indexed data. Deleting the
data directory is separate and does not delete original documents. Indexed
text, paths, collections and backup copies are plaintext.

## Validation and limits

The release pipeline must verify the exact release commit through full CI,
five native package jobs, an extracted Linux browser journey, installed VSIX
checks and complete manifest/checksum verification before publication.
Automated native-opening checks use controlled executable adapters; they do not
certify every external viewer or OS menu. On 2026-10-14 the owner reported the
short desktop acceptance check passed, including menu launch, opening originals
and restoring a saved collection. Exact OS/viewer details and independent manual
GUI coverage on other platforms are not established by that report. See
`docs/release-smoke-alpha4.md`.

This remains an unsigned alpha. Code signing, notarization, automatic updates,
OCR, semantic retrieval, private GitHub and team authentication are not included.
macOS builds require macOS 13 or newer. Public GitHub refresh is manual.
Local folder search uploads no documents and has no runtime telemetry; an MCP
client can forward returned passages to its chosen model provider.
