# Findrail README assets

## Published alpha.5 demonstration

`search-alpha5.png`, `collection-alpha5.png` and `freshness-alpha5.png` are
direct Chromium captures of the published Linux amd64 alpha.5 binary. The
30-second `workflow-alpha5.mp4` is a short sequence of direct browser captures
of actual UI states. It shows synthetic `kestrel` search and PDF page 2
provenance, a persisted named collection after process restart, and a refreshed
`cobalt` note. No personal profile or documents were used. The capture tooling
is from the repository; the demonstrated application is the public binary.

`capture-alpha5.json` records release revision, schema, archive/binary and
fixture hashes, date, OS, browser and tool versions. The verified Linux amd64
archive SHA-256 is `78885728107e26be6db18e9b9e19a5a8f0` (see the release
verification record); binary SHA-256 is
`6fc6fe384171a39d3c2809bdf8e923a089cee21516b04a5d3dfb419e0e011a96`.
The combined new media is under 1 MiB (15 MiB maximum). Video editing is limited
to selecting UI states and ordinary cuts; it is not a benchmark or native
acceptance evidence.

Reproduce after extracting and independently verifying the Linux amd64
`v0.1.0-alpha.5` release archive. Create metadata with its published version,
source revision `cb53ee9638f69b47b386921e41368f3b66ceddde`, schema 8, archive
SHA-256 and extracted binary SHA-256, then run:

```sh
python3 -m pip install selenium==4.35.0
python3 scripts/alpha5_media.py /path/to/extracted/findrail /path/to/verified-asset.json /path/to/output
```

Chromium, matching ChromeDriver and ffmpeg are capture dependencies, not
application dependencies. The script never builds Findrail. Its temporary
profile, fixture, index and server are cleaned up on success or failure.

Contributor instructions: [CONTRIBUTING.md](../../CONTRIBUTING.md).

## Branding

`findrail-mark.svg` and the light/dark wordmarks are original vector assets.
They extend the rounded, two-line mark already used by the application header,
using Findrail's existing green and paper palette. They contain no third-party
logos, embedded fonts, scripts or external resources. The wordmarks use a system
sans-serif font and the README selects a variant with a `picture` element.
They are distributed under the repository's Apache-2.0 license.

## Published alpha.3 screenshots

`overview-alpha3.png`, `search-alpha3.png` and `preview-alpha3.png` are direct browser captures of the
actual published Linux amd64 `0.1.0-alpha.3` binary running the built-in
three-document demo on 2026-10-14. The archive checksum was verified against
the release before extraction:

```text
b8e9d606e6487745a9f96d4453114e7ef6ec74fcde242a7da20a059462b3b9e9
```

The source release is
[`v0.1.0-alpha.3`](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3),
revision `d5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb`, schema 6.
The screenshots use fictional Markdown, Go and PDF documents. Temporary paths
belong to the isolated demo. No personal documents or normal index were used.

To reproduce the state, run `./findrail demo`, search `idempotency`, then
open **Preview** on `webhook-runbook.pdf`. Check **Page 2 / 2**. The PNGs are
browser screenshots, with no composited UI or added results. They demonstrate
search and PDF evidence, not performance or alpha.4 desktop/collection behavior.

The capture ran in [README media workflow 38511760352](https://github.com/Lephiziel/findrail/actions/runs/38511760352).
Reproduce it with `python3 scripts/readme_media.py /path/to/published/findrail
--output /path/to/captures`, using Selenium 4.35.0 and a matching Chrome/driver,
or run **README media (published alpha.3)** in GitHub Actions. The workflow has
read-only repository permissions and uploads screenshots as an artifact;
it does not commit files, change a release or publish an index.

## Historical media

`demo.mp4`, `demo.gif` and `demo.png` retain the older alpha.2 recording.
They should not be labeled as alpha.3 or alpha.4 demonstrations. See
[demo instructions](../demo.md).
