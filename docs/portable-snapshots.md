# Portable source snapshots

A portable snapshot transfers **indexed plaintext**, not original
PDF/DOCX files and not a Findrail database backup. PDF page text and source
provenance are included; omitted extraction features (including OCR) cannot be
restored. Import must not reconnect to the origin or read its URI.

## Format v1

The ZIP has no prefix/comment/trailing payload and contains exactly these three
entries: `manifest.json`, `documents.jsonl`, and `pages.jsonl`. Document paths
are JSON values and never ZIP entry names. JSONL records are one JSON object per
newline, ordered by relative path; page records are ordered by path and
one-based page number. ZIP entry times are normalized. The manifest identifies
the format, version, producer, informational UTC export time, original source provenance,
original extraction policy (including GitHub repository/ref/path/full commit and
DOCX policy where applicable), exact document/page counts, uncompressed payload
sizes and SHA-256 hashes. Document records preserve original ID as provenance,
relative path, title, URI, media type, complete text, original content hash,
size, modification time and page count. Destination document IDs and content
hashes are newly derived during import. Page records carry the original one-based
number and full text. PDF document body follows Findrail's stored convention:
each page's text followed by one newline, including the final page.

The v1 manifest's exact top-level fields are `format`, `version`, `producer`,
`exported_at`, `origin`, `document_count`, `page_count`, `documents_bytes`,
`documents_sha256`, `pages_bytes`, `pages_sha256`, and `fingerprint`. The `origin`
object uses `id`, `kind`, `name`, `location`, `indexed_at`, `max_text_bytes`,
`max_pdf_bytes`, and `max_docx_bytes`; applicable GitHub provenance adds
`repository_url`, `owner`, `repository`, `full_commit_sha`, `ref_mode`,
`ref_value`, `selected_path`, `github_max_bytes`, `github_policy_version`, and
`commit_time`. A document record uses `id`, `path`, `title`, `uri`, `media_type`,
`text`, `content_hash`, `size_bytes`, `modified_at`, and `page_count`. A page
record uses `path`, `number`, and `text`. V1 rejects unknown or missing fields.

The semantic fingerprint is SHA-256 over UTF-8 concatenated length-prefixed
fields in this order: domain `findrail-snapshot-fingerprint-v1`, origin ID,
kind, name, location, original successful indexing timestamp, text, PDF and
DOCX policy integers, GitHub max bytes, GitHub policy version, GitHub commit
time, document/page counts, document payload hash and page payload hash. Each
field is encoded as decimal UTF-8 byte length, `:`, then its bytes. Export time
and producer are intentionally excluded. A matching
fingerprint detects consistency; it does not authenticate an author or sign the
archive.

The codec enforces hard caps: 256 MiB compressed input/output, 512 MiB expanded
input, 256 KiB manifest, 10,000 documents, 100,000 pages, 16 MiB per JSONL
record, and 8 MiB text per document/page. Additional caps are 2,048 UTF-8 bytes
and 512 code points per relative path, 4,096 bytes per title, 8,192 bytes per
URI, 512 bytes per source/import name, 2,000 pages per document, and JSON depth
32. Operations default to two minutes; `--timeout` accepts 1s–10m. Validation
rejects unsafe schemes/authorities, non-pinned GitHub URIs, traversal paths,
duplicate keys/records, and inconsistent PDF/page data. Future versions are
rejected rather than guessed.

Export takes a single SQLite reader snapshot and stages the ordered records. It
does not scan files, migrate the source index or contact connectors. Output must
be new, its parent must already exist, and output inside the index or any
registered filesystem root is refused. Publication never replaces an existing
file or symlink. On Unix the archive and staging files use mode 0600; Windows
uses normal account filesystem protection and these POSIX mode bits are not an
ACL claim.

Archives contain plaintext content and provenance. Original locations can be
absolute paths and may include usernames; no secret-redaction guarantee is
made. Protect files using the operating system's account permissions and avoid
sharing archives through untrusted channels. Integrity hashes are not
encryption or authentication.

## CLI journey

Use real source IDs shown by `findrail sources --json`:

```bash
findrail export-source --data-dir INDEX_A --source SOURCE_ID --output 'notes.findrail.zip' --json
findrail inspect-export 'notes.findrail.zip' --json
findrail import-source --data-dir INDEX_B --name 'Notes snapshot' --json 'notes.findrail.zip'
findrail search --data-dir INDEX_B --source IMPORTED_SOURCE_ID --mode advanced '"retry budget"'
findrail start --data-dir INDEX_B --no-open
findrail forget --data-dir INDEX_B IMPORTED_SOURCE_ID
```

`inspect-export` validates the entire archive but does not authenticate its
author. By default it hides original source name/location and all document
names; `--show-paths` reveals only original source name/location. It never prints
document or page text. Import can target an existing index or a new directory.
After a CLI import to a running `start` data directory, the Sources panel and
search source selector observe it through normal inventory reloads; no restart
or browser upload is needed. Source builds implement this journey; existing
alpha.2 release archives are unchanged and do not acquire these commands.

PowerShell:

```powershell
findrail.exe export-source --data-dir 'C:\Data\Index A' --source SOURCE_ID --output 'notes.findrail.zip' --json
findrail.exe inspect-export 'notes.findrail.zip' --json
findrail.exe import-source --data-dir 'D:\Index B' --name 'Notes snapshot' --json 'notes.findrail.zip'
findrail.exe search --data-dir 'D:\Index B' --source IMPORTED_SOURCE_ID --mode advanced '"retry budget"'
findrail.exe forget --data-dir 'D:\Index B' IMPORTED_SOURCE_ID
```

An imported source is a frozen independent source, not current/up-to-date. It
must not be watched, refreshed, configured as a folder, or contacted over the
network. MCP access remains opt-in by explicitly allowing its destination source
ID. Logical removal deletes the imported searchable rows, not separate archive
copies or secure remnants in storage media.

The destination source ID has a separate versioned namespace derived from the
verified semantic fingerprint. Re-importing an identical fingerprint reports
`already_imported` and preserves the existing display name. Changed snapshots
coexist; they do not replace the old snapshot or a live source with matching
paths. Imported rows store the original hash only as provenance; the destination
hash is computed with `findrail-import-content-v1`, media type, exact indexed
body, and page texts. The archive is never used as a deduplication key for live
documents.

An invalid archive, integrity failure, or cancellation during archive preparation
does not create or migrate an index. After full validation succeeds, normal
destination initialization/migration may begin; a later cancellation or storage
failure can leave a newly initialized empty index, but never a partial source.
Schema upgrades are forward-only; back up the stopped index before
upgrading and use a compatible Findrail version for older-schema restoration.

## Small resource spot check

One local Linux amd64 run used an 8,388,608-byte synthetic UTF-8 text document
(the per-document v1 text cap), exported from a one-document SQLite source.
With Go `go1.27.2-X:nodwarf5`, export took 0.081 seconds, produced a 44,950-byte
compressed archive, and reported a peak process RSS of 71,368,704 bytes from
`/proc/<pid>/status` `VmHWM`. This is one synthetic spot check, not a scale or
cross-platform performance claim. Cancellation was verified with pre-canceled
codec contexts and operation cleanup logic; elapsed mid-operation cancellation
was not separately benchmarked.
