# Findrail 0.1.0-alpha.5

Alpha.5 extends local search with passage windows, paged results, portable
source workflows, and explicit evidence transfer. The app runs locally with
its SQLite index and embedded browser interface; Go, Python and a database
server are not runtime requirements.

## Highlights

- **Passage windows** show matching text with surrounding indexed context,
  including late Markdown and Word passages. PDF citations retain original
  page numbers; DOCX previews show body text rather than Word layout or pages.
- **Paged results** support First/Previous/Next and page sizes 20/50/100 within
  the 1,000-result window. An index change requires an explicit restart of
  that result window. Search presets preserve configuration; Apply leaves
  searching explicit and keeps pinned evidence and unsaved notes.
- **Evidence bundles** transfer exact saved passages, notes, order and offsets
  into a new collection after explicit source mapping and conflict checks.
  Markdown citation export remains available.
- **Portable source jobs** export, inspect and import indexed-text snapshots
  through explicit paths on the Findrail host. Imported sources are independent
  frozen snapshots. They contain indexed text and provenance, rather than
  original files, saved collections or a complete index backup.
- **Freshness and first-run guidance** distinguish indexed snapshots from current
  originals, preserve usable results after a failed refresh, and show unsaved
  collection changes separately from operational messages. Original actions use
  Open original / Open containing folder consistently. Narrow layouts and the
  keyboard save/Quit/reopen path have automated browser coverage.
- **Recovery and installation checks** exercise abrupt-stop/reopen integrity,
  complete old-or-new index state, native per-user install/update/uninstall and
  data preservation. Explicit diagnostics export and `doctor --integrity` remain
  local; integrity checks do not repair the index or certify search relevance.

Desktop launch, opt-in original opening, saved collections, public GitHub file
snapshots, read-only stdio MCP and the optional VS Code client remain available.

## Installation, upgrade and rollback

The distribution contains five native app archives, the optional VSIX,
`release-manifest.json` and `SHA256SUMS.txt`. Verify the matching checksum,
extract into a directory you own and follow `INSTALL.txt`. macOS requires 13+.
The application uses **schema 8**, **Client API 1** and extension **0.1.0**.

Stop every Findrail process and back up the **whole data directory**, including
WAL/SHM files when present, before upgrading an existing index. Alpha.4 uses
schema 7 and cannot open schema 8. Rollback restores a complete stopped
pre-upgrade backup to a separate directory and uses the matching old binary;
there is no reverse migration. Portable snapshots and evidence exports are
not database backups. Uninstalling desktop resources preserves indexed data
and original documents.

## Validation and limits

The release pipeline requires exact-head Go/vet/race/UI checks, five native
package targets, extracted packaged browser and MCP journeys, installed VSIX,
upgrade/restore, integrity and complete asset verification before publication.
The owner reported the supplied Linux source-candidate acceptance procedure
passed on 19 October 2026; its exact provenance and scope are recorded in
[platform installation](https://github.com/Lephiziel/findrail/blob/main/docs/platform-installation.md).
This is not a manual Windows or Apple Silicon first-run report.

A pre-release Linux measurement on 100,000 synthetic documents recorded warm
HTTP search p95 of **201 ms**, above the **150 ms engineering target**. It used
only ten measured requests and has no same-host comparison; it is not a latency
SLO or proof of a regression. See the candidate identities, measured limits
and raw reports in the
[resource baseline](https://github.com/Lephiziel/findrail/blob/main/docs/resource-baseline.md).

This remains an unsigned alpha. Manual Finder/SmartScreen, external-viewer and
assistive-technology coverage is separate from native CI. The local index and
exported copies contain plaintext. `serve`/`demo` remain read-only with temporary
collections; management and persistent collections are available in writable
start/desktop sessions. Native opening remains opt-in. OCR, semantic search,
private GitHub, team authentication and automatic updates are not included.
