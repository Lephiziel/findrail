# Manual Windows and Apple Silicon acceptance kits

The acceptance kits make first-run feedback reproducible without Go or Python on
the tester's computer. They are forms and small fictional documents, **not** a
new application release. The builder verifies an already-downloaded release set;
it never builds or executes the selected platform binary.

## Build from verified assets (offline)

Obtain the release assets separately through a trusted browser/download stage.
Keep exactly the eight files named by the release contract together:
`SHA256SUMS.txt`, `release-manifest.json`, five target archives, and the VSIX.
Do not import `installer_lifecycle_smoke.py` for download work: that native smoke
has platform/network side effects and is not a download helper.

The builder uses `release_assets.verify`, `package.verify_archive`,
`package.extract_verified`, and their existing safe-name/checksum/BUILD-INFO
rules. The input directory must contain the exact release inventory. Output is
an atomically written ZIP outside (and not containing) the asset directory.

```bash
python3 scripts/acceptance_kit.py --assets-dir /abs/verified-release \
  --target windows-amd64 --output /abs/kits/windows-alpha5.zip
python3 scripts/acceptance_kit.py --assets-dir /abs/verified-release \
  --target darwin-arm64 --output /abs/kits/apple-silicon-alpha5.zip
```

For the published alpha.5 set the builder requires version `0.1.0-alpha.5`,
source SHA `cb53ee9638f69b47b386921e41368f3b66ceddde`, schema 8 and Client API 1.
In addition, `published_release` requires exact matches for the published
manifest SHA-256 (`28ed088d…9031b7`) and `SHA256SUMS.txt` SHA-256
(`f587ffc5…d781423`). The existing strict validator then verifies the complete
inventory and each archive against those published metadata bytes. Matching the
source SHA alone is not sufficient: a rebuilt/repacked archive with that same
SHA is `source_candidate`, has no published release URL, and requires an explicit
`--delivery-method` (for example, `Actions artifact`). `candidate.json` records
the actual archive size/hash, manifest/checksum hashes and fixture hash. The
builder does not claim the current checkout SHA is the binary's source SHA. It
does not change `build/package/VERSION`, release-readiness requirements or
release assets.

## Kit contents and manual scope

Each archive contains `START_HERE.md`, `EXPECTED_RESULTS.md`, `candidate.json`,
`REPORT.md`, `report.json`, and three deterministic synthetic fixtures: Markdown,
a minimal DOCX and a two-page text PDF. The distinct words include an expected
page-2 PDF match. DOCX search/preview does not require Office. No macros, external
links, personal documents, or executable files are included.

The eight report cases retain the readiness contract's exact IDs and order:
`install`, `launch`, `repeat-launch`, `retrieval`, `collection-restart`,
`refresh`, `quit`, `uninstall-data-preserved`. All start as `not_run`, elapsed
seconds as unknown, and observations blank. Uninstall checks use only the
isolated test installation/data and preserve the original documents. Native
original-opening or folder reveal is optional and separately identifies the
viewer; missing a viewer is not a passed case.

The testers obtain the **original** app asset from the identified source in a
browser and install it normally (Explorer/Windows flow or Finder/macOS flow).
Installing a copy extracted from the kit would not preserve browser download,
quarantine or Gatekeeper semantics. Kits explicitly do not recommend disabling
OS protections globally. A CI native run, cross-build, or automated browser test
is not a manual GUI, SmartScreen, or Apple quarantine observation.

The kit permits optional testing with one's own documents, but does not ask for
queries or file/index uploads. An outcome and anonymized blocker suffice. There
is no automatic upload, email, telemetry, credentials, or hidden network call.
Keep reports local unless the tester chooses a safe channel, and redact personal
paths, account names and document details before sharing.

## Validation and interpretation

Run the focused tests with:

```bash
python3 -m unittest scripts/test_acceptance_kit.py
```

Generated archives can be inspected with standard ZIP tools. Fixture validation
on Linux (including extracting/searching the DOCX/PDF with the matching Linux
application binary) is useful but must be reported as Linux fixture validation,
not Windows/macOS acceptance. Manual outcomes remain pending until an actual
person runs the matching candidate on the named native platform and records the
real environment, security prompts, duration if measured, and result. A completed
alpha.5 report never transfers to a different source SHA or candidate.
