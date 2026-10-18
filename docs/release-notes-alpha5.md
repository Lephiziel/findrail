# Findrail 0.1.0-alpha.5 release notes (prepared)

These notes describe the prepared alpha.5 contract, not a published release.
Until the owner authorizes publication, alpha.4 remains the current download.

## What's new

- Passage windows show matching text with surrounding indexed context; PDF
  citations retain original page numbers. DOCX files do not have Word page
  numbers in Findrail.
- Search results have First/Previous/Next navigation and 20/50/100 page sizes,
  within a bounded 1,000-result window. A changed index requires an explicit
  restart of the result window. Preset files preserve search configuration;
  preview, explicit source mapping and **Apply** do not run a search.
- Evidence bundles transfer selected exact passages, notes, ordering and offsets
  into a new saved collection after explicit source mapping and confirmation.
  Markdown citation export remains available. Source changes and conflicts are
  checked before an atomic import.
- The Sources UI can export, inspect and import portable indexed-text snapshots
  through local jobs and explicit paths on the machine running Findrail. Imported
  sources are frozen and work independently of the originals; this does not
  upload a browser file. A snapshot contains indexed text and provenance, not
  original files, saved collections or a complete data-directory backup.
- Diagnostics can be inspected and explicitly downloaded without uploading a
  report. `doctor --integrity` adds bounded structural and foreign-key checks;
  it does not repair an index or certify full FTS consistency or search quality.

## Installation and upgrade

The prepared contract remains five native archives plus the optional VSIX,
`release-manifest.json` and `SHA256SUMS.txt`. The application uses schema **8**,
Client API **1** and extension **0.1.0**. No Go, Python or database server is
required to run an archive. Follow its `INSTALL.txt`; public download links are
switched only after publication and verification.

Before opening an existing index, stop every Findrail process and back up the
**whole data directory**, including WAL/SHM files when present. Alpha.4 uses
schema 7 and cannot open schema 8. Rollback restores the full stopped pre-upgrade
backup to a separate directory and uses the matching old binary; there is no
reverse migration. Uninstalling desktop resources preserves the data directory
and original documents. Portable snapshots and evidence exports are not backups.

## Boundaries

The preview displays indexed text. The original viewer opens the current original
file, which may have changed since indexing. This is an unsigned alpha; automated
coverage applies only to recorded environments and synthetic fixtures. Manual
Finder/SmartScreen/external-viewer coverage is separate from native CI. The local
index and exported copies contain plaintext. `serve`/`demo` remain read-only with
temporary collections; management is available in `start`/desktop. Native opening
remains opt-in. OCR, semantic search, private GitHub and automatic updates are not
included.
