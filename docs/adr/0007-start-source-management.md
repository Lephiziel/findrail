# ADR 0007: opt-in source management in `start`

Status: accepted, 2026-10-07.

## Context

The local UI previously exposed only read operations. First-run onboarding and
source changes required separate CLI commands. Making the UI writable broadens
the loopback boundary, so the controller must be composed deliberately and must
not leak into `serve`, `demo`, MCP, or ordinary read-only API handlers.

## Decision

`internal/sourceapp` is a small application service composed by `internal/cli`
for `start`. It owns in-memory jobs and process-derived operation contexts.
There are at most two running jobs, sixteen queued jobs and one hundred
completed-history entries. Active entries are retained. Each job has a bounded
deadline; GitHub requests have a two-minute operation timeout. Jobs retain only
bounded status/result/error strings, not file contents or archives. Shutdown
stops acceptance, cancels queued/running work, waits for cleanup, then allows the
watcher and store to stop. No durable job log or replay is introduced.

The source service composes existing filesystem connector, ingestion, PDF child
process, GitHub parser/resolver/archive preparation and atomic store publication.
It does not execute source content. GitHub requests happen only for explicit Add
and Refresh actions. The UI uses bounded polling for in-memory jobs.

Only `start` supplies the management controller to HTTP. `serve` and `demo` do
not register mutation routes. Existing read endpoints and response envelopes
remain unchanged. `/api/v1/capabilities` advertises the boundary. The process
creates a random in-memory capability; `/api/v1/session` returns it with
`no-store`. Every mutation requires an exact `http://<bound authority>` Origin,
the custom `X-Findrail-Token` header compared in constant time, a non-cross-site
Fetch Metadata value, JSON for mutations, a 16 KiB strict decoder, and an empty
body for source DELETE and job cancel. Bound authority comes from the actual
listener, not a client Host header. No CORS, remote bind, or account system is
added. This is a defense against hostile web origins, not against another
process running as the same OS user.

Filesystem updates use the persisted source configuration and
`ingest.Refresh`/`BeginRefresh`. The `start` service and watcher share a
process-local per-source coordinator around filesystem scans and removal; it
does not cover unrelated sources or GitHub network preparation. The existing
SQLite writer transaction and `BeginRefresh` registration check remain
authoritative across separate processes. A refresh started before a forget
either publishes before the forget transaction (which then deletes it), or
observes the missing registration and cannot recreate it. Explicit `index` or
Add after removal remains a new registration. GitHub publication continues to
require the saved registration token and revision. Preparation happens outside
GitHub publication transactions; stale downloads cannot overwrite a forget or
newer snapshot.

Remove is an application job. It cancels/waits for an overlapping UI operation,
then calls the atomic logical `ForgetSource`. The deletion point is the SQLite
delete transaction; successful completion means the source and cascaded
documents/FTS entries are gone. Original files are never deleted. A stale
preview/citation is cleared in the browser on successful deletion.

## Consequences

- `start` creates a normal empty SQLite index and serves the first-run UI.
- Source management is source-build functionality until a later release archive
  includes it.
- Local mutation clients must retain the session token in memory and send Origin
  on every mutation; missing/null Origin is rejected.
- Job state is intentionally lost on restart; committed source snapshots remain
  durable.
- The read-only `serve`, demo and MCP boundaries remain intact.
