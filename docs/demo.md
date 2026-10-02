# Try Findrail with three fictional documents

The demo contains a Markdown note, a small Go example and a two-page text PDF.
It uses no accounts, external services or private documents. The
[19-second walkthrough](assets/demo.mp4) renders real CLI and API results on
these files; it is not a browser screen recording. Pauses are edited for
readability, so the video is not a performance benchmark.

## Start

Download and extract a [release archive](https://github.com/Lephiziel/findrail/releases).
Download this repository using GitHub's **Code → Download ZIP**, then extract it.
In a terminal inside the repository folder, replace `/path/to/findrail` below
with the executable from the release archive:

```bash
/path/to/findrail index --data-dir .findrail-demo examples/demo
/path/to/findrail serve --data-dir .findrail-demo
```

On Windows, use the path to `findrail.exe`, such as
`C:\Tools\Findrail\findrail.exe`. In PowerShell, prefix a quoted executable path
with `&`. Use the same data directory in both commands. Go is only needed if
you choose to build the executable yourself.

Open **http://127.0.0.1:7766** and search for **idempotency**. There should be
three matching documents. Select **Preview** on `webhook-runbook.pdf`: the
match is on **page 2 of 2**. **Copy location** includes `#page=2`; it does not
launch an external PDF viewer.

## Watch an edit appear

Search for **cobalt**: the initial demo has no match. In
`examples/demo/retry-notes.md`, change `Demo marker: amber` to
`Demo marker: cobalt` and save. While `serve` is running, the result should
appear automatically. Folder notifications normally trigger a refresh within
a few seconds; periodic scans provide a fallback. Restore `amber` afterward
if you want to repeat the original demo.

Ctrl+C stops the server. The demo index is separate from your normal index.
Original documents are never changed by Findrail.

## A useful first test

After the demo, choose a folder you already know. Try finding two fragments
whose words you remember and check their original locations. Report your OS,
Findrail version, whether installation succeeded, and what failed or helped.
Use the [alpha feedback issue](https://github.com/Lephiziel/findrail/issues/3)
or the bug-report template. Share only synthetic or redacted reproductions.

This alpha uses literal keyword search. It does not include OCR, semantic
retrieval, cloud connectors or a desktop launcher.

## Reproduce the media

Developers can run `python3 scripts/demo_media.py bin/findrail` from the
repository after building the executable. This optional media tool requires
Pillow, ffmpeg and DejaVu fonts; these are not application dependencies. It
creates a temporary synthetic folder and index, verifies search and PDF page
attribution, runs a loopback server, checks an automatic update, and stops it.
Generated PNG, GIF and MP4 files go to `docs/assets/`. No personal folders are read.
