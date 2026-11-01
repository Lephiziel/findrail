# Data handling and trust boundaries

## Current behaviour

- Local files are indexed only from explicit roots passed to `index`, `start`, or
  selected in the `start` Sources panel. The browser accepts an absolute path; it
  does not upload files or invoke a native folder picker.
- Public GitHub files are downloaded only by explicit `index-github` or
  `refresh-github`; retrieval reads the committed local snapshot.
- Portable source archives are explicit CLI exports of indexed text and
  provenance, not original attachments or database backups. They are plaintext
  and may expose original paths/usernames; no secret-redaction guarantee is made.
- The index contains extracted text, locations, source names, and timestamps.
- In writable `start`, explicitly saved named evidence collections add selected
  passages, provenance, ordering and user notes to the same local SQLite data
  directory. This plaintext is included in a stopped full-directory backup, but
   not in portable source exports. Published alpha.3 uses schema 6; published
   alpha.4 uses schema 7. Current alpha.5 source candidates use schema 8, which
   stores passage-window offsets; this source behavior is not part of the alpha.4
   download.
- Runtime local search does not call a hosted model, analytics service, or source API.
- The explicit **Diagnostics** action reads a bounded whitelist of application
  version, runtime platform, schema compatibility and aggregate policy/source
  counts. The JSON excludes paths, source/document identities and names, document
  text, queries, tokens, environment, arguments, logs and raw database errors.
  It is downloaded only after the user selects **Download report**; Findrail
  never uploads it or polls a remote service.
- The database is plaintext, protected by local filesystem permissions rather than encryption.
- Common credential-like names, hidden entries, and symlinks are skipped. This is not a universal secret detector.
- Document contents are treated as text and never executed.
- DOCX body extraction is bounded, offline, in-memory and stores plaintext; the
  index is not a sanitization/redaction tool. Unsupported Word features are omitted.
- HTTP is loopback-only. `serve` and `demo` remain read-only. `start` additionally
  exposes source mutations guarded by the actual bound authority, exact Origin,
  process-memory CSRF capability, Fetch Metadata and strict bounded JSON. The
  capability is not persisted; it does not protect against same-user processes.
- `start --enable-originals` optionally exposes a token-protected same-origin POST
  action. The server resolves a document ID against the current source registration
  and indexed content identity, then launches an allowlisted current filesystem
  file through fixed OS commands. The client cannot supply a path, URL, executable
  or command arguments. Search and preview GETs never launch applications; `serve`,
  `demo`, MCP and VSIX do not enable native opening.
- PDF/DOCX use the OS-configured opener; text/source files use a fixed editor where
  supported. Spawn success means an attempt, not proof of display. A positive PDF
  page must exist in indexed evidence, but page navigation is not requested from
  an unknown viewer. Opened content is the current file; preview and citations stay
  tied to the indexed snapshot.
- `forget` removes searchable source data logically. Old disk / WAL / backup bytes may remain.
- Imported archive sources are frozen, offline SQLite data; removal does not
  delete exported archive copies or guarantee physical erasure.
- The last successful indexing report is stored locally with the committed
  snapshot. Optional examples contain bounded relative names; sensitive-name
  paths are redacted. Reports are not part of portable exports and are not telemetry.

## User controls

Choose a dedicated index directory, inspect `sources`, and remove a source with
`forget` or the confirmed `start` Sources action. Removal clears only local
searchable records. Original files stay untouched. Deleting the complete dedicated data
directory while Findrail is stopped removes the active index; secure disk erasure
depends on the OS and storage device.

## Connected adapters

Public GitHub file snapshots use no credentials and refresh only on explicit
Add/Refresh actions; routine search, preview, citations, polling and MCP read the
committed local snapshot. Future
private adapters need explicit scopes, credential vault integration, visible sync
status, deletion propagation, and revocation handling. API permissions at the
source must determine what can be indexed. Private-data support is not unlocked
just by accepting a token string.

## Planned AI access

An optional embedding / model service needs an explicit explanation of data
transmission. MCP clients receive only scoped, bounded retrieval results.
Source content is untrusted input: it may contain instructions, but those
instructions do not authorize actions. Findrail's initial AI-facing interface
will be read-only.

## Residual limits

Other processes running as the same OS user may access the local database or
loopback server. A malicious plugin, compromised account, or copied index can
expose data. A future encrypted-storage feature needs a separate key-management
design. The current browser interface does not provide network or multi-user
deployment security.
Path checks immediately before launch cannot eliminate the operating-system race
where another local process replaces a file between validation and use by the OS
opener. Opening may fail if a fixed editor/opener is unavailable. Imported archive
provenance is frozen and never opened as a live original; pinned public GitHub
permalinks remain a separate explicit browser action.
