# Human smoke sheet: alpha.3 (10–15 minutes)

Run once on one computer you have access to; this is not a five-OS certification
matrix. Use only a document you are comfortable indexing locally, or create a
small synthetic technical note and text PDF. Do not send private text, file paths,
screenshots containing private data, or unredacted logs to anyone.

## Before the run

- [ ] Use the candidate archive actually provided for this run; do not treat
  alpha.3 as publicly released unless the release page confirms it.
- [ ] Verify the archive hash against the SHA256SUMS.txt delivered with that
  candidate/release. Record OS/architecture and candidate SHA/version locally.
- [ ] Extract to a user-owned directory whose path contains a space if convenient.
- [ ] Choose a new dedicated test data directory; do not point this smoke at a
  personal Findrail index. Keep this path for the restart step below.
- [ ] Stop if checksum verification fails. Do not disable system-wide OS security
  to launch an unsigned binary.

## One workflow

1. Start from the extracted directory with a fresh, dedicated empty data
   directory (the quoted examples deliberately include spaces):
   - Linux/macOS: `./findrail start --data-dir "$HOME/Findrail alpha3 smoke data" --no-open`
   - Windows PowerShell: `& ".\findrail.exe" start --data-dir "$env:USERPROFILE\Findrail alpha3 smoke data" --no-open`
   Confirm it prints a loopback URL; open it in a browser.
2. Choose **Add folder** and select/enter the test folder. Confirm no folder is
   indexed before explicit selection.
3. Search one technical term you know occurs in a text document. Open **Preview**
   and verify the matching text and source. If using a text PDF, verify the shown
   page number against the page in your fixture.
4. Add two exact passages from the visible indexed evidence to the collection,
   attach a short note, and download the Markdown. Check the downloaded file's
   citations and note without sharing its contents.
5. Stop with Ctrl+C, then restart with the exact same `--data-dir`; reopen the UI
   and confirm it starts and the indexed source is
   still available. Confirm the in-tab collection cleared after reload (expected).
6. Stop every Findrail process with Ctrl+C. Optionally remove the test data only
   after confirming it is the fresh test index, not a personal index.

## Private local result

Mark each step pass / fail / not run locally. If reporting an issue, share only
candidate SHA/version, OS/architecture, step number, and a sanitized error code
or synthetic reproduction. Do not upload documents, query text, absolute paths,
logs, diagnostics with paths, or collection exports. This single human run does
not certify other platforms, browsers, editors, Gatekeeper, or the release.

The older [demo recording](assets/demo.mp4) shows the narrower alpha.2 demo
workflow; it is not evidence for this smoke path. Optional media work tracked
separately is not a release blocker.

The owner's 2026-10-13 local smoke and the separate automated release checks
are recorded in [release verification](release-alpha3-verification.md).
