# Release candidate pipeline

The alpha release workflow is a preparation pipeline until the repository owner
intentionally changes `build/package/VERSION` on `main`. A push to `main` runs
the full reusable CI, five native package jobs, a real packaged browser journey,
installed VSIX checks, exact-set aggregation, reusable verification, and a
read-only published-release state check. Only after every
gate succeeds can the final job create a release. Pull requests rehearse the
same gates with publication disabled and preview app version alpha.5 while
`build/package/VERSION` remains alpha.4. Publication is
restricted to the canonical repository's main push or explicit main dispatch;
PR and fork previews have read-only permissions. Alpha.4
archives include native desktop launchers and target-specific install
resources; these are archive members, not additional release assets.
Current source schema is 8 after migration 008; the public alpha.4 remains
schema 7 until the owner decides to publish another release.

## Preview rehearsal

For an unpublished alpha.5 candidate, use **Actions → Package rehearsal
(no release) → Run workflow** with `candidate_sha` set to the exact commit SHA and
`candidate_version` explicitly set to `0.1.0-alpha.5`. A rehearsal never edits
`build/package/VERSION`; its archive is not a published release.
The workflow checks out that SHA (not a PR merge ref), builds through
`scripts/package.py --version 0.1.0-alpha.5 --require-clean`, and executes each
extracted CLI and desktop binaries from a temporary path containing spaces and
Unicode. All five native targets exercise first/second desktop launch and PDF
child extraction through the packaged launcher using a
controlled native executable adapter on each target OS; these are lifecycle
checks, not manual viewer demonstrations. macOS uses the packaged app entrypoint;
Windows uses the GUI executable. Linux additionally migrates a synthetic schema-2
fixture and a schema-6 index created by the actual published alpha.3 binary.
The restored schema-2 fixture is checked
through SQLite; the restored schema-6 backup is opened by the published alpha.3
binary. It also creates a schema-7 index using the
published alpha.4 binary, including a committed indexing report and an actual
saved collection with notes and order. Candidate migration checks search,
provenance, report retention, and legacy passage start zero. A complete stopped
backup is opened by the same alpha.4 binary; alpha.4 must reject schema 8 without
modifying it. An induced failed migration must roll back byte-for-byte. Linux
also runs a small, same-host synthetic resource comparison against published
alpha.4; this is not a search-quality claim. The native Linux job also runs the
generated-config SDK integration against the extracted candidate executable via
`FINDRAIL_MCP_TEST_BINARY`; it does not substitute a freshly rebuilt source CLI.
The packaged browser journey tests native
original/folder actions through controlled local executable adapters and verifies
named collection restoration only after the candidate process stops and restarts.
It cannot
create or move tags, edit VERSION, or publish a release. The five artifacts are
named `candidate-<sha>-<target>` and retained for 14 days. A full asset rehearsal
uses **Alpha release candidate → Run workflow** with that SHA and `publish=false`;
set `candidate_version=0.1.0-alpha.5`; its `release-assets-<sha>` aggregate is
retained for 30 days. This path accepts only the exact head SHA of `main`, does
not edit VERSION, and cannot publish when `publish=false`.

Download the artifact from the run summary, extract it, then verify the complete
candidate set with the same revision and version:

```sh
python3 scripts/release_assets.py verify \
  --directory ./release-assets \
  --version 0.1.0-alpha.5 \
  --revision <candidate-sha>
```

The native artifacts contain only their archive and a small verification report;
no temporary indexes, user paths, credentials, or test databases are uploaded.

## Shared integrated browser gate

Standalone `packaged-journey.yml` and the mandatory Alpha release candidate
browser job both call `scripts/packaged_journey.py` with the same verified
extracted Linux executable. The shared case list includes the existing preset
and evidence-bundle browser smokes plus `portable_transfer_browser_smoke.py` for
cross-feature A/B/C transfer, note/cursor fences and uncertain commit outcomes.
The latter requires TASK 11 Sources UI in main; a missing dependency fails the
case and does not certify TASK 12. See [the alpha.5 acceptance inputs and
commands](release-smoke-alpha5.md#integrated-candidate-gate).

An explicit workflow preparation step builds the existing offline synthetic
GitHub fixture writer. The orchestrator requires its path for the collection
smoke and records its digest separately; it never runs `go run` or builds/selects
a replacement application during acceptance.

The JSON report records only executed cases and their durations, including the
failed case. Transfer subcases are embedded in the orchestrator report. Identity
failure stops before invoking smokes; an explicit browser skip produces an
`incomplete` report with skipped cases, and a missing browser fails. Timeouts
reap the complete child process group; stdout/stderr are drained to temporary
files with bounded diagnostic tails. Both workflow artifact steps run even on
failure, retaining `packaged-journey-<sha>` for 14 days and
`release-browser-journey-<sha>` for 30 days respectively. No indexes, browser
profiles or raw logs are uploaded. A failed browser gate still prevents
aggregation/publication. There is no second release pipeline.

## Published alpha.3 asset contract

The immutable published alpha.3 contains exactly these eight files (no staging extras):

```text
findrail_0.1.0-alpha.3_linux-amd64.tar.gz
findrail_0.1.0-alpha.3_linux-arm64.tar.gz
findrail_0.1.0-alpha.3_darwin-amd64.tar.gz
findrail_0.1.0-alpha.3_darwin-arm64.tar.gz
findrail_0.1.0-alpha.3_windows-amd64.zip
findrail-readonly-0.1.0.vsix
release-manifest.json
SHA256SUMS.txt
```

The VSIX version remains the extension's own `0.1.0`; it is not renamed to the
application alpha version. Manifest asset digests and sizes are generated from
the final archive/VSIX bytes. `SHA256SUMS.txt` covers all six installation files
and `release-manifest.json`, but not itself. The manifest has this shape (the
actual generated values are authoritative):

```json
{
  "manifest_version": 1,
  "app_version": "0.1.0-alpha.3",
  "source_revision": "<40-character candidate commit SHA>",
  "schema_version": 6,
  "client_api_version": 1,
  "extension_version": "0.1.0",
  "assets": [
    {
      "basename": "findrail_0.1.0-alpha.3_linux-amd64.tar.gz",
      "kind": "archive",
      "target": "linux-amd64",
      "size": 12345678,
      "sha256": "<64-character SHA-256>"
    },
    {
      "basename": "findrail-readonly-0.1.0.vsix",
      "kind": "vsix",
      "size": 123456,
      "sha256": "<64-character SHA-256>"
    }
  ]
}
```

The published manifest is pinned to the historical alpha.3 tag SHA
`d5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb`, schema 6, API 1 and extension 0.1.0.
The alpha.4 contract is pinned to schema 7, API 1 and extension 0.1.0.
Its manifest revision must match the actual immutable tag; all archives'
BUILD-INFO records must match that revision, version and schema. Packaging
derives the built schema from source and checks the contiguous migration
inventory; a schema change requires a new version contract. Unknown
candidate versions and malformed metadata fail closed. The generated manifest
includes the five platform archives and the one VSIX.
Archive bytes are not compared across operating systems/toolchains. Native
verification checks BUILD-INFO revision/version/target/cleanliness and invokes
the CLI version command. Candidate archives use strict per-target member
allowlists for the CLI, desktop launcher, that target's install resources, build
metadata, product notices and bounded license inventory; historical alpha.3
retains its original archive member set. Extraction rejects traversal,
duplicate/conflicting paths, links and special files; launcher executable modes
are checked. Linux additionally runs schema-2, schema-6, and published schema-7
upgrade/restore checks, plus a same-host resource comparison against alpha.4.
The Linux browser job consumes that exact native archive, starts an
empty index, adds a folder in the UI, checks selected text/PDF-page citations
and Markdown download, and checks reload/restart. The installed client job runs
extension tests and compiled-server integration, then installs the actual VSIX
in isolated user-data/extensions directories and exercises it against the
extracted native candidate. Only the test bootstrap is loaded as a development
extension; Findrail must load from the installed extension directory.

## Gates and immutable-release behavior

Publication requires the exact same workflow run's full CI (including three-OS
Go checks, race and UI tests), five native package jobs, packaged browser journey,
installed VSIX checks, aggregate verification, and reusable final verification.
The generic CI workflow is additional evidence, not a substitute for these
required jobs. `release-state` has read-only contents permission and rejects API
errors, a tag without a release, an incomplete asset set, or a mismatched
tag/manifest revision. For the new contract, existing archives' embedded
BUILD-INFO and the VSIX identity are also verified before accepting a no-op.
A complete historical alpha.2 release is a no-op when
main advances with VERSION unchanged; its established contract is five archives
plus `SHA256SUMS.txt`, without a VSIX or manifest. The publisher has write
permission only in the final job, rechecks that the tag does not already exist,
and refuses to replace or move an existing tag/release.

A complete alpha.4 release is a no-op after main moves, including when source
checks package an unpublished alpha.5 candidate. The gate checks
schema 7, the exact eight-file set, all remote checksum bytes, embedded archive
provenance and the VSIX identity against the release's tag revision, rather
than today's main revision or a later source schema. The historical alpha.3
SHA/schema contract remains unchanged.

## Intentional alpha.4 publication

PR and main-source checks package alpha.5 while `VERSION` remains alpha.4. The
candidate schema is read from the checked-out source; alpha.3 and alpha.4 retain
their historical schemas 6 and 7. These alpha.5 checks do not publish: the
publisher still requires the candidate version to equal `VERSION`, and the
PR and main-push read-only release-state gate checks alpha.4 against its
historical tag SHA and exact existing assets, returns `noop` even though source
HEAD is newer. On a PR, preview publish is false and the publisher is skipped;
on a main push, the candidate alpha.5 does not equal `VERSION` alpha.4 and the
publisher is also skipped.
Dispatch rehearsals require the exact current main SHA and `publish=false` for
preparation. Alpha.5 is not accepted as an existing published-release contract.
At the current source schema 8, alpha.5 remains an unpublished preview and does
not redefine the alpha.4 schema-7 release contract. The migration checks pin the
published alpha.4 Linux amd64 archive to SHA-256
`41f91c62b618a64dc083f7a63403f7f4fcc6f8b13ffb613acfee6704927f6a1a`; mismatch
fails closed rather than updating the reference silently.

After the new release exists, [verify the public downloads](release-smoke-alpha4.md#publication-after-acceptance)
before switching README download links and announcing it. A future source
schema needs a new explicit candidate/version contract, not an edit to the
immutable alpha.4 contract.
