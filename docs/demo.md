# Try Findrail with three fictional documents

The demo contains a Markdown note, a small Go example and a two-page text PDF.
It uses no accounts, external services or private documents. The
[37-second screen recording](assets/demo.mp4) shows the running web interface
using these files: keyword search, PDF page 2, a note edit and automatic
refresh. It contains no added captions or end card. The MP4 preserves the
original 1920×1080 video frames; its empty audio track was removed and the file
was arranged for playback before the download completes. The GIF is a smaller
preview of the search and PDF steps. This demonstration is not a performance
benchmark.

## Start

Download and extract the `0.1.0-alpha.2` archive for your platform from
[Releases](https://github.com/Lephiziel/findrail/releases). Inside the extracted
folder, run:

```bash
# Linux / macOS
./findrail demo
```

```powershell
# Windows
.\findrail.exe demo
```

The binary includes all three documents; a repository download and Go are not
needed. Findrail prints the temporary document folder and attempts to open your
browser. If no browser appears, open the printed local URL manually. Use
`demo --no-open` on a headless machine, or `demo --addr 127.0.0.1:7767` if the
default port is busy.

Each run creates fresh documents and an isolated index. Normal shutdown removes
the workspace; a forced process kill can leave it in the OS temporary directory.
Edits to the demo files are discarded on normal shutdown. The original example
files also remain available in `examples/demo` for repository users. On older
`0.1.0-alpha.1` binaries, download the repository and run `index --data-dir
.findrail-demo examples/demo`, then `serve --data-dir .findrail-demo`.

Open **http://127.0.0.1:7766** and search for **idempotency**. There should be
three matching documents. Select **Preview** on `webhook-runbook.pdf`: the
match is on **page 2 of 2**. **Copy location** includes `#page=2`; it does not
launch an external PDF viewer.

For a Markdown citation, select the sentence in the page 2 preview and choose
**Copy as Markdown**. Paste it into a note: the selected passage appears in a
text code block with the PDF title, `file:` source and `#page=2`. With no
selection, the button copies the whole visible page preview instead.

## Watch an edit appear

Search for **cobalt**: the initial demo has no match. In
`retry-notes.md` in the temporary document folder printed by `demo`, change `Demo marker: amber` to
`Demo marker: cobalt` and save. While `demo` is running, the result should
appear automatically. Folder notifications normally trigger a refresh within
a few seconds; periodic scans provide a fallback. Restore `amber` afterward
if you want to repeat the original demo.

Ctrl+C stops the server. The demo index is separate from your normal index.
To search your own files after stopping the demo, use `findrail start DIRECTORY`.
Original documents are never changed by Findrail.

## A useful first test

After the demo, choose a folder you already know. Try finding two fragments
whose words you remember and check their original locations. Report your OS,
Findrail version, whether installation succeeded, and what failed or helped.
Use the [alpha feedback issue](https://github.com/Lephiziel/findrail/issues/3)
or the bug-report template. Share only synthetic or redacted reproductions.

This alpha uses literal keyword search. It does not include OCR, semantic
retrieval, cloud connectors or a desktop file opener.

## Prepare a screen recording

Record the browser while following the search and edit steps above. Use only
fictional documents. Save the full recording as `docs/assets/demo.mp4`, a GIF
preview as `docs/assets/demo.gif`, and a still from the recording as
`docs/assets/demo.png`. Keep the README and launch description consistent with
what the video actually shows.

## Optional CLI walkthrough

Developers can run `python3 scripts/demo_media.py bin/findrail` from the
repository after building the executable. This optional media tool requires
Pillow, ffmpeg and DejaVu fonts; these are not application dependencies. It
creates a temporary synthetic folder and index, verifies search and PDF page
attribution, runs a loopback server, checks an automatic update, and stops it.
Generated PNG, GIF and MP4 files go to `dist/rendered-demo/`. These optional
illustrations are separate from the recorded web demo and do not overwrite it.
No personal folders are read.
