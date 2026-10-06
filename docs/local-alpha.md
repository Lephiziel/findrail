# Local alpha behaviour

## Automatic refresh

`start` and `serve` start refresh workers for registered filesystem sources. It scans at
startup, listens to native directory notifications and reconciles every five
minutes by default. New folders are watched after discovery. Events are
coalesced with a 350 ms debounce and a two-second maximum debounce window.
Notifications are hints, including rename / deletion / overflow; only a
successful full inventory authorizes pruning.

`--sync-interval` changes periodic reconciliation; `--no-sync` disables workers
in the HTTP process. `watch` runs the same workers without HTTP. Sources added
or forgotten through another CLI process are discovered every two seconds.
Refresh transactions never recreate a forgotten source. Do not run multiple
refresh processes unnecessarily; writes serialize even across SQLite users.

One writer and a separate four-connection read pool use WAL snapshots. Searches
and previews see committed data during extraction. A missing or unreadable
root, failed extraction or cancellation rolls back; the old snapshot stays
searchable. Failed jobs retry with backoff up to one minute; periodic checks and
new events can trigger an earlier retry. Jobs and status are in memory and
rebuilt with a complete scan after restart, not a durable remote-sync queue.

On filesystems without native notifications, periodic scans remain enabled.
A source has a budget of 8,192 watched directories. Watch setup failures appear
in `/api/v1/sync` and the UI. The periodic full scan still reads and hashes every
eligible file. Large collections therefore need performance measurements before
assuming low resource use.

## PDF text

The connector opens a selected PDF within `os.OpenRoot` and streams it to a
child of the same Findrail executable. No shell or document-provided executable
is invoked. The child has a ten-second deadline, bounded input / output, at most
500 pages and 1 MiB total text. Its `GOMEMLIMIT=128MiB` is a soft Go GC target,
not a hard memory cap or OS security sandbox. The parser may allocate more
memory while processing a page; hostile-file isolation remains a beta task.

Input defaults to 16 MiB, configurable with `--max-pdf-bytes` from 0 (disabled)
to 32 MiB. The extraction text budget is independent of `--max-bytes`. Image-only
and text-budget-exceeding PDFs are skipped and included in skipped totals.
Invalid / encrypted PDFs fail the source inventory; no partial changes commit.
OCR and faithful visual PDF rendering are not implemented.

Search matches all query terms anywhere in a document, including its title.
For PDFs the returned page is the best matching page for one or more query
terms. When terms span pages, a single excerpt may not contain every term.
A title-only match has no matching page and opens page 1 by default.

## Preview

`GET /api/v1/documents/{id}` reads the indexed snapshot. PDFs accept `?page=N`;
page numbers are one-based. The response contains a content hash, file
modification time, source and URI. Text is limited to 65,536 Unicode characters
per preview. Truncation is explicit. An unknown ID returns 404; invalid pages
return 400. This endpoint never accepts a filesystem path or reads an original
file. The UI displays plain text, safely highlights query words and offers PDF
page navigation. If files change before reindexing, the preview still shows the
version that was indexed.

`Copy location` copies only the original address. `Copy as Markdown` copies the
selected passage from the current preview, or the whole current preview when
nothing useful is selected. For PDFs, both the citation text and its `file:`
source retain the current page. The result is always an indexed snapshot; the
browser does not read the original file again. A truncated preview is marked in
the citation, and the same Markdown is shown in a read-only field when the
Clipboard API is unavailable or refuses the copy. The local `file:` address
preserves provenance, but it is not a public link and may not open in every
Markdown client.

## Upgrade

Stop Findrail and back up its dedicated data directory. Migration 2 preserves
schema 1 documents, adds media metadata, PDF pages / page FTS and source limits.
The migration commits atomically. Old sources stay text-only until explicitly
re-indexed, preserving their previous inclusion policy. The old binary rejects
schema 3; restore the stopped-directory backup to downgrade.

## Alpha release scope

Checksummed archives target Linux amd64 / arm64, macOS amd64 / arm64 and Windows
amd64. Cross-compilation is not full runtime certification. CI runs tests and
the compiled journey on Linux, macOS and Windows; inspect the linked workflow
for its actual outcome. Binaries are unsigned and unnotarized. Search relevance,
100k-document scale, OCR and cloud sources are not established by these checks.

## Built-in demo

`demo` embeds three fictional documents and copies them to a new private
temporary workspace. Documents and the demo index are sibling directories,
so the index is excluded from ingestion. It delegates indexing, refresh and
serving to `start`. It never resolves the normal data directory and accepts
no personal folder or `--data-dir` override. Each invocation starts fresh.

A normal shutdown stops HTTP and refresh workers, closes the store, and then
removes the workspace. A forced kill or power loss can leave it in the OS
temporary directory. Its path is printed so you can edit the synthetic note
while recording or testing. Demo edits are discarded when the workspace is
removed. Use `start DIRECTORY` for persistent personal sources.
