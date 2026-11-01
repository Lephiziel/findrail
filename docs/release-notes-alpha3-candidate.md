# Findrail 0.1.0-alpha.3 — candidate notes (not published)

**Historical preparation record.** This file describes the pre-release candidate.
Alpha.3 was subsequently [published](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3); use the
[actual release notes](release-notes-alpha3.md) and
[release verification](release-alpha3-verification.md) for its final assets and SHA.

## Candidate additions

- Empty-index first start and local folder management in the web UI; bounded DOCX
  body-text indexing is opt-in for existing/legacy sources.
- Public, commit-pinned GitHub file snapshots; read-only MCP; opt-in Advanced
  lexical queries and shared format/path/title filters.
- Portable snapshots transfer indexed plaintext and provenance, not original
  files or a frozen copy of the database. Imported sources are independent and
  frozen, not refreshed imports.
- Bounded indexing diagnostics: the report is the last committed successful
  scan, not an attempt history. Root-only `.findrailignore` rules can exclude
  files; a failed policy scan retains the prior committed index/report.
- In-tab evidence collections can keep two chosen passages and notes and export
  Markdown; reloading the tab clears the collection.
- Read-only `evaluate` compares literal result path sets for 1–100 cases, at most
  100 results per case. Incomplete cases are errors; mismatches return nonzero
  only after the complete report is emitted.
- Experimental connector kit and a separate read-only VS Code VSIX. The VSIX
  requires Findrail server/client API v1; it is not a Marketplace/Open VSX
  release and independent connector adoption is not established.
- Release preparation adds archive provenance/checksum instructions and schema-6
  upgrade/rollback guidance.

## First-run path

After a real release is published: download the matching standalone archive,
verify its SHA256 checksum, extract it, start with an empty index, choose **Add
folder**, find a technical document, inspect Preview/PDF page, add two exact
passages with a note, download Markdown, then restart. See the
[manual smoke sheet](release-smoke-alpha3.md). Do not substitute MCP, GitHub,
VS Code, or a source build as a prerequisite.

## Upgrade and limits

Published alpha.2 uses schema 2; alpha.3 candidate code uses schema 6 with
migrations 1–6. Stop every process and back up the whole data directory including
WAL/SHM before upgrading. Legacy alpha.2 folders retain DOCX disabled until
explicitly enabled; other existing source policies are preserved. Reverse migrations do not exist; rollback restores the whole backup to a separate
location and uses a binary compatible with its schema. An alpha.2 binary must
never open schema 6. Index content and provenance are plaintext. Scanned PDFs have
no OCR; retrieval is lexical, not semantic. See the
[candidate rehearsal report](release-candidate-alpha3.md) for evidence and
unrun checks. Native packaged execution does not certify interactive browsers,
installed editors, macOS Gatekeeper/Finder, or every platform user experience.

## Separate VSIX distribution

The VSIX is a separate local installation, not bundled in the standalone
application archive. It requires VS Code 1.95+ and a compatible API-v1 Findrail
server on loopback. It is not published on Visual Studio Marketplace or Open VSX.
Do not claim either listing exists.

## Not included

No OCR, semantic retrieval, private GitHub, automatic update, code signing,
notarization, Marketplace/Open VSX publication, or stable-release guarantee.
