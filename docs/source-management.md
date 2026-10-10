# Source management from the local web UI

The browser source manager is included in source builds. Existing release
archives do not include it yet. It is enabled by `findrail start` only; the
ordinary `serve` command and the isolated `demo` remain read-only.

## First run and folders

Start with an empty data directory (or omit `--data-dir` to use the platform
default):

```sh
findrail start --data-dir ./findrail-data --no-open
```

Open the loopback URL printed by the command. The empty index is ready for
explicit source selection. Findrail does not scan home directories, Documents,
or disks. Choose **Add a source → Add folder** and enter the absolute path on the
machine running Findrail. A browser folder picker would only expose files to the
browser; it cannot choose a server-side path, so Findrail does not pretend to
offer one.

Examples:

- Linux: `/home/alex/notes`
- macOS: `/Users/alex/Library/Notes`
- Windows: `C:\Users\Alex\Documents\Notes`

Folder defaults match the CLI: 1 MiB per text file, 16 MiB per PDF, and 8 MiB
per DOCX in source builds. DOCX is disabled for existing folders until explicitly
re-indexed with a DOCX-enabled policy. DOCX is a bounded plain-text body snapshot;
see [the supported subset](docx.md). Hidden entries, symlinks, credential-like
filenames and the index directory remain excluded. Originals are read-only from
Findrail's perspective. Adding the same canonical folder again reports that it
already exists; use **Refresh** to update it. Watch notifications and periodic
reconciliation continue through the existing manager.

Source reports show the last committed scan diagnostics. See
[indexing diagnostics](indexing-diagnostics.md) for explicit path details,
coverage limits, archive availability and troubleshooting.

Filesystem source cards offer **Configure** for the DOCX enabled/input-limit
policy. Applying the change queues a bounded re-index. The new policy and full
inventory commit in one SQLite transaction; failure or cancellation preserves
both the previous policy and searchable snapshot. Configure, watcher refresh,
manual refresh and removal share the source coordinator. Registration tokens
and revisions additionally reject stale operations across independent SQLite
handles and forget/re-add cycles.

## Public GitHub snapshots

Choose **Add a source → Public GitHub repository** and enter `OWNER/REPO` or a
`https://github.com/OWNER/REPO` URL. Ref and relative POSIX subdirectory are
optional; an empty path (or `/`) selects the repository inventory. Public
repositories only: Findrail resolves the selected ref to a full SHA and fetches
one bounded archive for that SHA. It does not clone, execute repository code,
request credentials, or poll GitHub automatically. **Refresh** is an explicit
request. Search, preview and citations read only the last committed local
snapshot and work offline.

An operation shows its queued/running phase and process-local progress (actual
processed documents and skipped entries; no percentage) and can be canceled.
Successful progress names the durable report ID; failed/canceled progress stays
partial and separate from the last committed card summary. Failure,
cancellation before publication, or shutdown preserves the previous committed
snapshot. A refresh for one source is not duplicated while busy. Search keeps
reading the committed snapshot while preparation is in progress.

## Imported archive snapshots

Use the CLI-first transfer flow in [portable snapshots](portable-snapshots.md).
An imported archive appears in a running `start` session's Sources and search
selectors through the existing inventory. It is not scanned, watched, refreshed
or configured. The panel displays its historical original location, original
successful indexing time and local import time. Only explicit removal is
available. Import never automatically adds its ID to an MCP allowlist.

**Remove from index** requires confirmation. It removes the source's local
searchable records, not original files, and is logical deletion rather than
secure erasure. If a UI operation is running, removal cancels/waits for its
cleanup before committing deletion. Folder scans from `start` and its watcher
share a per-source process coordinator; cross-process refresh remains guarded
by storage registration checks and SQLite transactions. An already prepared
refresh cannot re-register a removed source; GitHub publication retains its
registration-token/revision compare-and-swap guard.

## Command boundaries

- `start`: writable source manager, plus local search/preview and existing folder
  watcher. The mutation API is protected by same-origin checks and an in-memory
  process token. It is not authentication against other local processes.
- `serve`: read-only browser/API access to an existing index.
- `demo`: temporary synthetic documents, isolated workspace, and read-only UI;
  added user sources are not retained.

Loopback HTTP clients that mutate sources must send an exact `Origin` matching
the bound authority and the `X-Findrail-Token` received from `/api/v1/session`.
Requests without Origin (including `Origin: null`) are rejected. The token is
process-scoped, delivered only in a no-store response, and is not persisted.
