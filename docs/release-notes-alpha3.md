# Findrail 0.1.0-alpha.3

Findrail is a local-first search app for notes, code and technical documents.
Alpha.3 adds empty-index startup and folder selection in the browser, with
indexed-text Preview and PDF-page evidence. Choose a folder, find a technical
document, inspect its source, save two passages with a note, and download the
Markdown citations. Go, Python and an external database are not required at
runtime.

## Highlights

- Bounded offline DOCX body-text extraction, opt-in Advanced queries and
  source/format/path/title filters. DOCX previews have no page-number claims.
- Manual commit-pinned public GitHub text snapshots and a scoped, read-only
  stdio MCP server. Private GitHub and remote serving are not included.
- Portable source snapshots preserve indexed plaintext and provenance. They do
  not contain original files or a frozen database; imported sources are
  independent and frozen.
- Source reports show the last committed successful indexing run, not a history
  of failed attempts. Root-only `.findrailignore` rules are supported; a failed
  policy scan retains the previous committed content/report.
- Evidence collections are in-tab memory and disappear on reload. Explicit
  Markdown download is separate and can contain private text/paths.
- The read-only `evaluate` command checks Literal result path sets for 1–100
  cases, with up to 100 results per case. Incomplete cases are errors; mismatches
  return nonzero after the complete report.
- Five standalone archives, shared `SHA256SUMS.txt`, `release-manifest.json`, and
  a separate read-only VS Code VSIX. Extension version is `0.1.0`; it requires a
  running Findrail server/client API v1 and is not a Marketplace/Open VSX release.
- Connector conformance tooling remains experimental; independent adoption is
  not established.

## Install and upgrade

Download the archive for your OS/CPU and `SHA256SUMS.txt` from this release,
verify the matching checksum, and extract it. Run `./findrail start` on
Linux/macOS or `.\findrail.exe start` in Windows PowerShell, then open the
printed loopback URL and choose **Add folder**. Read the bundled `INSTALL.txt`.

Alpha.2 uses schema 2; alpha.3 uses schema 6 with forward migrations 1–6. Stop
all Findrail processes and back up the entire data directory, including SQLite
WAL/SHM when present, before upgrading. Legacy sources keep DOCX disabled until
explicitly enabled. Reverse migrations do not exist. Never open schema 6 with an
older binary: rollback restores the whole stopped backup to a separate location
and uses a compatible binary. To uninstall, stop all processes and remove the
extracted app directory; remove the data directory separately only if its
plaintext indexed copy is no longer needed. Original files are untouched.

## Validation and limitations

The release pipeline rebuilds and checks all five native packages at the release
commit, exercises the extracted Linux archive in Chromium, checks schema-2 → 6
upgrade/rollback and runs the installed VSIX against that archive. The owner
also reported successful local indexing/search/Preview with Word documents and
repository files. That report does not certify every platform or interaction.

Findrail stores indexed text and provenance as local plaintext. OCR, semantic
retrieval, private GitHub, automatic updates, code signing and notarization are
not included. Archives are unsigned; macOS may require an app-specific security
allowance. Public GitHub refresh remains manual. The VSIX needs a separate
running server. Native package checks do not certify every browser/editor/OS
security prompt.

[Published release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3) · [Verification](release-alpha3-verification.md)
