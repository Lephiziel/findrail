# Platform installation reports

These are reports of specific alpha releases and installation paths, not a
stable-release or platform-wide certification. Release archives include
`INSTALL.txt`; start there and use only fictional documents for a first test.

## Apple Silicon: `0.1.0-alpha.2`

Tested on 2026-10-08 on an actual Apple Silicon Mac running macOS 27.0.1
(build 26A434), architecture `arm64`. The published archive was used, not a
locally rebuilt executable. Go and an external database were not required.

### Download and verify

The tested download path used GitHub CLI:

```bash
gh release download v0.1.0-alpha.2 --repo Lephiziel/findrail \
  --pattern 'findrail_0.1.0-alpha.2_darwin-arm64.tar.gz' \
  --pattern SHA256SUMS.txt
shasum -a 256 findrail_0.1.0-alpha.2_darwin-arm64.tar.gz
```

The digest matched the archive's entry in the release's `SHA256SUMS.txt`:

```text
2eb190abd0d10978a93d130c2fa38fa8dfbfb2b6aacc824afeed646b793ffb98
```

Extract into a directory you own, enter the extracted folder, and read its
installation instructions:

```bash
tar -xzf findrail_0.1.0-alpha.2_darwin-arm64.tar.gz
cd findrail_0.1.0-alpha.2_darwin-arm64
./findrail version
./findrail demo --no-open --addr 127.0.0.1:17766
```

`version` printed `findrail 0.1.0-alpha.2`. The alternate loopback port avoided
using the default port; `--no-open` made browser opening explicit. Open the
printed local URL manually. Plain `./findrail demo` is the documented shortcut
that attempts to open the browser; automatic browser opening was not tested here.

### Observed demo results

- The embedded fictional Markdown, Go and two-page PDF documents indexed
  successfully: three documents, none skipped.
- Searching `idempotency` in the web interface returned all three documents.
- Previewing `webhook-runbook.pdf` showed **Page 2 / 2** and the idempotency
  passage. The displayed original location ended in `#page=2`.
- `cobalt` initially had no result. Changing `Demo marker: amber` to
  `Demo marker: cobalt` in the temporary `retry-notes.md` printed by the demo
  made that document appear on a subsequent search without restarting or
  manually reindexing. Refresh was asynchronous: an immediate API request
  still returned zero before the later web search returned one.
- The marker was restored to `amber`. A normal interrupt stopped the demo
  and removed its temporary document and index workspace.

The browser used for the checks was Codex's in-app browser. No personal folders,
accounts, cloud services, normal Findrail index, or screenshots were used in
this report. The demo server remained bound to loopback throughout.

### Unsigned-alpha limitations

The release is unsigned and not notarized. This GitHub CLI download followed by
Terminal extraction had no `com.apple.quarantine` attribute on the executable;
only `com.apple.provenance` was present. No approval dialog appeared and no
security settings or file attributes were changed.

This does **not** establish the first-run experience of a Safari/Finder download
with quarantine metadata. As `INSTALL.txt` explains, macOS may require an
explicit, application-specific allowance in Privacy & Security according to
your system policy. That approval path was not exercised in this run. Do not
disable system-wide protections to run the alpha; signing and notarization
remain release limitations, not completed stable-installation work.
