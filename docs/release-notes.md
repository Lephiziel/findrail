Findrail 0.1.0-alpha.2 makes the first local search easier to try.

- `findrail demo` includes three fictional documents: Markdown, Go code and a
  two-page text PDF. No repository download or personal folder is needed.
- Each demo has its own temporary documents and index. Normal shutdown removes
  them; your regular index is separate. A forced kill can leave temporary files.
- `findrail start DIRECTORY` indexes a selected folder, runs live refresh and
  attempts to open the browser. Later, `findrail start` resumes stored folders.
- Search guidance explains literal words and multiple-word matching. Matching
  words are highlighted in result snippets as well as previews.
- Failed browser-opener startup prints a manual URL and leaves search available.

Extract your platform archive and run `./findrail demo` (Linux / macOS) or
`.\findrail.exe demo` (PowerShell). Search for `idempotency` and preview the PDF
match on page 2. Stop with Ctrl+C; use `start DIRECTORY` for your own notes.
Go is not required. See `INSTALL.txt` inside the archive.

This remains an alpha for feedback. Archives support Linux amd64/arm64, macOS
amd64/arm64 and Windows amd64. Binaries are unsigned; macOS may need an explicit
security allowance. PDFs have input, page, text and time budgets. Scanned PDFs
need OCR, which is not included. The index stores plaintext and original paths.

Keyword search, page attribution, indexed-text previews, source health and a
read-only loopback API remain available. Failed source scans preserve prior
results. There are no runtime analytics or document uploads. The database schema
is unchanged from alpha.1. Cloud sources, DOCX, OCR, semantic search, a desktop
file opener and MCP are not included.
