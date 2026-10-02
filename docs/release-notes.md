Findrail's first local alpha finds remembered words in your selected notes,
source code and text PDFs, with saved-text previews and original locations.

- Automatic refresh while `serve` or `watch` is running, with periodic recovery.
- PDF page attribution and preview navigation; scanned PDFs need OCR, not included.
- Search remains available during indexing; failed scans preserve previous results.
- Source health, a read-only loopback API and CLI JSON output.
- Five installation archives with checksums and bundled license notices.

Extract your platform archive, run `findrail index /path/to/notes`, then
`findrail serve` and open http://127.0.0.1:7766. On Windows use `findrail.exe`.
Go is not required. See `INSTALL.txt` inside the archive.

This is an alpha for feedback. Binaries are unsigned; macOS may need an explicit
security allowance. Text PDFs have input, page, text and time budgets. The index
stores plaintext and paths. Existing foundation sources stay text-only until
re-indexed. Back up the stopped data directory before schema 2 migration.

Cloud connectors, DOCX, OCR, semantic search, desktop launching and MCP are
planned. They are not in this release.
