# Portable source snapshots

> Implementation status: the v1 codec and integrity inspector are under
> development. The complete CLI export/import journey is not yet available.

A portable snapshot is intended to transfer **indexed plaintext**, not original
PDF/DOCX files and not a Findrail database backup. PDF page text and source
provenance are included; omitted extraction features (including OCR) cannot be
restored. Import must not reconnect to the origin or read its URI.

## Format v1

The ZIP contains exactly `manifest.json`, `documents.jsonl`, and `pages.jsonl`.
Document paths are JSON values and never ZIP entry names. JSONL records are
ordered by relative path; page records are ordered by path and one-based page
number. ZIP timestamps are normalized. The manifest identifies the format,
version, producer, informational UTC export time, original source provenance,
counts, uncompressed payload sizes and SHA-256 hashes.

The semantic fingerprint is SHA-256 over UTF-8 concatenated length-prefixed
fields in this order: domain `findrail-snapshot-fingerprint-v1`, origin ID,
kind, name, location, original successful indexing timestamp, text, PDF and
DOCX policy integers, document/page counts, document payload hash and page
payload hash. Each field is encoded as decimal UTF-8 byte length, `:`, then its
bytes. Export time and producer are intentionally excluded. A matching
fingerprint detects consistency; it does not authenticate an author or sign the
archive.

The codec enforces hard caps: 256 MiB compressed input/output, 512 MiB expanded
input, 256 KiB manifest, 10,000 documents, 100,000 pages, 16 MiB per JSONL
record, and 8 MiB text per document/page. Import/export command limits and full
path/URI validation are still pending. Future versions must be rejected rather
than guessed.

Archives contain plaintext content and provenance. Original locations can be
absolute paths and may include usernames; no secret-redaction guarantee is
made. Protect files using the operating system's account permissions and avoid
sharing archives through untrusted channels. Integrity hashes are not
encryption or authentication.

## Planned CLI journey

When implemented, use real source IDs shown by `findrail sources --json`:

```bash
findrail export-source --data-dir INDEX_A --source SOURCE_ID --output 'notes.findrail.zip' --json
findrail inspect-export 'notes.findrail.zip' --json
findrail import-source --data-dir INDEX_B --name 'Notes snapshot' --json 'notes.findrail.zip'
findrail search --data-dir INDEX_B --source IMPORTED_SOURCE_ID --mode advanced '"retry budget"'
findrail start --data-dir INDEX_B --no-open
findrail forget --data-dir INDEX_B IMPORTED_SOURCE_ID
```

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

On validation failure, import must not create or migrate an index. A later
storage failure may leave a newly initialized empty index, but never a partial
source. Schema upgrades are forward-only; back up the stopped index before
upgrading and use a compatible Findrail version for older-schema restoration.
