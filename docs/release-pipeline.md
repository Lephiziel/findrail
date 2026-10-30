# Release candidate pipeline

Candidate production and publication are separate operations. Pull requests
(including forks), ordinary pushes to `main`, and workflow dispatches without an
explicit promotion request run the required CI/native/browser/VSIX/verification
gates in read-only mode. A push remains useful when `VERSION` and the candidate
both say beta.1: it cannot create or change a release or tag. This workflow has no
`workflow_run` trigger.

The only write path is the final job of an explicit `workflow_dispatch` on the
canonical repository's `main`, requested by owner `Lephiziel`, with
`publish=true`, `scope=limited-desktop-beta`, the exact current 40-character
candidate SHA, `candidate_version=0.1.0-beta.1`, and `VERSION=0.1.0-beta.1`.
It also requires the exact candidate, performance, and evidence-handoff run
identities (`ID/ATTEMPT`) plus the accepted root handoff artifact digest. Every
input defaults to non-publishing/empty. A dispatch with `publish=false` can run
the read-only Go `verify` path; no draft or tag is created. A missing/mismatched
artifact digest, unknown scope/version, stale main, fork run, or different run
attempt blocks before any write.

The small `cmd/beta-promote` Go maintainer tool drives the path. It reuses the
bounded `beta_evidence_github.py preflight`, `beta_evidence.py verify`,
`release_assets.py verify`, and the read-only `release_gate.py` contract; the
Go tool owns request validation, temporary staging, and the only write path.
Run IDs, attempts, required jobs, artifact IDs/digests/expiry, and the approved
root artifact digest are checked before promotion. Technical native,
browser/VSIX/MCP/upgrade/restore/recovery/scale/series/soak proofs must pass.
Human evidence may remain pending and `final_release=false`; failures or missing
technical proof block. The owner packet must show the exact notes/body SHA-256
and all eight asset names, sizes, and SHA-256 values printed by `verify`.

The tool promotes the accepted eight asset bytes without rebuilding or
reassembling the application. Notes are the exact `publication-notes.md` bytes
from the verified handoff. The final job alone has contents write permission. It
creates a draft with no assets, uploads only the eight explicit filenames,
downloads and verifies their bytes/IDs/digests/notes/tag target, then rechecks
live `main` immediately before the public transition. Publication uses
`make_latest=false`; it never clobbers, deletes, or recreates a release/tag.
Partial writes remain as drafts and are recorded in the audit artifact; there is
no automatic repair or cleanup of remote state.

The read-only release-state check runs for candidate previews too. A complete
published beta.1 after later documentation-only `main` movement is a no-op tied
to its historical tag and bytes; the summary names the published SHA and never
moves the tag to current `main`. A draft, partial release, tag/release mismatch,
asset collision, checksum/manifest/embedded identity mismatch, or non-404 API
error fails closed and is not automatically repaired. `VERSION` remains
alpha.5 in this task; beta.1 remains a preparation candidate until a separately
reviewed version change. See the [beta candidate contract](beta-candidate.md).
Alpha.4 archives include native desktop launchers and target-specific install
resources; these are archive members, not additional release assets.
The current public alpha.5 uses schema 8 after migration 008; historical alpha.4
remains schema 7. See the [published alpha.5 verification](release-alpha5-verification.md).

## Preview rehearsal

For a nonpublishing beta.1 rehearsal, use **Actions → Package rehearsal
(no release) → Run workflow** with `candidate_sha` set to the exact commit SHA and
`candidate_version` explicitly set to `0.1.0-beta.1`. A rehearsal never edits
`build/package/VERSION`; its archive is not a published release.
The workflow checks out that SHA (not a PR merge ref), builds through
`scripts/package.py --version 0.1.0-beta.1 --require-clean`, and executes each
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
set `candidate_version=0.1.0-beta.1`; its `release-assets-<sha>` aggregate is
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
The alpha.4 contract is pinned to schema 7, API 1 and extension 0.1.0. The
prepared alpha.5 contract is explicitly pinned to schema 8, API 1 and extension
0.1.0, independent of later source migrations. Preparation-only beta.1 also has
an exact schema-8/API-1/extension-0.1.0 contract, not a version range or a
published-release contract.
Each manifest revision must match its actual immutable tag; all archives'
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

Candidate runs require the full reusable CI (including three-OS Go checks, race
and UI tests), five native package jobs, packaged browser journey, installed
VSIX checks, exact-set aggregation, recovery, and reusable final verification.
The generic CI workflow is additional evidence, not a substitute for these
required jobs. Ordinary `main` pushes run those jobs with read-only release
state; matching `VERSION` and candidate versions do not select a publisher.
Evidence intake pins the exact current job graph: only `promotion-preflight` and
`promotion-publish` may be skipped on a read-only candidate. A separately listed
historical graph is accepted for already-produced handoffs; unknown, missing, or
unexpectedly executed write jobs fail closed.

The separate beta promotion requires the exact successful candidate `push`,
performance `workflow_dispatch`, and refreshed evidence-handoff
`workflow_dispatch` runs. Their current API run attempts, required job sets,
artifact IDs, source SHA, and GitHub artifact digests are rechecked before bytes
are accepted. The handoff is independently verified against the selected
candidate assets and raw technical proofs: five native targets and installer
cases, browser/VSIX/MCP/upgrade/restore, recovery, individual 10k/100k scale,
full series, and soak must pass. Only the human categories permitted by the
limited scope can remain pending; `readiness.py` remains `final_release=false`
and pending. A decision card or a JSON field such as `approved=true` is not
authorization, authentication, or a write token. The owner makes the separate
decision and then explicitly dispatches the workflow; TASK 36/B remains subject
to a direct owner instruction.

For the new contract, remote archives' embedded BUILD-INFO and VSIX identity are
verified before accepting a no-op. A complete historical alpha.2 release is a
no-op when main advances with VERSION unchanged; its established contract is
five archives plus `SHA256SUMS.txt`, without a VSIX or manifest. Alpha.3–alpha.5
retain their pinned schemas and checks. Beta.1 is pinned independently to schema
8, Client API 1, VSIX 0.1.0, five native archives and exactly eight release
files. The gate whitelists the version before interpreting paired 404 responses;
unknown beta.2/stable/v1 versions never become a publish decision. Only the
explicit final job has contents write permission.

A complete alpha.4 or alpha.5 release is a no-op after main moves. The gate checks
schema 7 for alpha.4 or schema 8 for alpha.5, the exact eight-file set, all remote
checksum bytes, embedded archive
provenance and the VSIX identity against the release's tag revision, rather
than today's main revision or a later source schema. The historical alpha.3
SHA/schema contract remains unchanged.

The beta.1 candidate uses the same eight staging assets and five targets.
Ordinary push release-state checks the immutable `VERSION`; a promotion dispatch
checks the beta.1 contract. A valid promotion does not use new builds from that
dispatch: the eight files come from the exact `release-assets-<sha>` artifact
named in the accepted handoff. The published alpha.5 Linux archive is separately
pinned for the beta-only same-schema compatibility case; it is not mixed into
beta manifests. Historical alpha.5 journey summaries retain 19 root cases;
beta.1 requires those 19 plus the same-schema case (20). See
[beta candidate](beta-candidate.md).

## Explicit promotion inputs (future frozen candidate only)

Do not use these example values now. A real dry-run or publish is valid only
after the future beta.1 `VERSION`/candidate SHA is frozen, all required runs and
the refreshed handoff exist, the handoff passes verification, and the owner has
reviewed that exact packet. The current task creates no tag, release, or draft.

In **Actions → Alpha release candidate → Run workflow**, select `main` and set
these exact values from the accepted handoff/card:

| Input | Required value |
|---|---|
| `candidate_sha` | Exact 40-character frozen main SHA; it must equal dispatch SHA and live `main` before writing. |
| `candidate_version` | `0.1.0-beta.1` |
| `scope` | `limited-desktop-beta` |
| `candidate_run` | Exact successful candidate push as `ID/ATTEMPT`. |
| `performance_run` | Exact successful performance workflow as `ID/ATTEMPT`. |
| `handoff_run` | Exact successful refreshed evidence handoff as `ID/ATTEMPT`. |
| `handoff_artifact_digest` | Exact `sha256:<64 lowercase hex>` root Actions artifact digest from that handoff. |

The read-only interface is:

```sh
go run ./cmd/beta-promote verify \
  --candidate-run '<candidate-run-id>/<attempt>' \
  --performance-run '<performance-run-id>/<attempt>' \
  --handoff-run '<handoff-run-id>/<attempt>' \
  --handoff-artifact-digest 'sha256:<accepted-root-artifact-digest>'
```

In the workflow, `GITHUB_*`, candidate/version/scope, `PUBLISH`, and read-only
GitHub tokens are supplied by Actions. The command downloads the exact artifacts,
checks run and artifact provenance/expiry, re-verifies the full handoff and all
eight assets, and prints the reviewed notes hash and per-asset hashes/sizes. The
root digest binds those nested bytes, so there are no duplicate notes/manifest/
checksums/inventory hash inputs. The output is review evidence, not authorization.

The workflow's final job invokes the same interface as `publish`, with the same
four reviewed values and an audit destination:

```sh
go run ./cmd/beta-promote publish \
  --candidate-run '<candidate-run-id>/<attempt>' \
  --performance-run '<performance-run-id>/<attempt>' \
  --handoff-run '<handoff-run-id>/<attempt>' \
  --handoff-artifact-digest 'sha256:<accepted-root-artifact-digest>' \
  --audit-file "$RUNNER_TEMP/promotion-audit/promotion-audit.json"
```

That subcommand additionally requires the workflow's `PUBLISH=true`, owner actor,
canonical `workflow_dispatch` context, and the final job's scoped write token.
This task does not invoke it against GitHub.

Run the workflow with `publish=false` first to verify inputs and proofs without
a write. After a separate owner decision on that exact packet, the final job uses
the same run identities and digest with `publish=true`. No `approved` JSON is
consumed. The Go tool refuses an old/fork/short SHA, a moved `main`, mismatched
attempts, expired artifacts, changed bytes/notes, failed/invalid evidence, or any
missing technical proof. It checks `main` once before draft creation and again
after upload/download/final draft verification, immediately before the public
transition. There is no API transaction across the `main` ref and release edit;
the final guard narrows but cannot eliminate that concurrent-update window. If
`main` moves, the draft remains private for manual owner review and the tool
stops without publishing or automatically repairing it.
An unpublished GitHub draft may not yet have a Git tag ref. In that state the
draft's API `target_commitish` must equal the accepted candidate SHA, and only a
specific 404 for that exact tag ref is treated as expected absence. Other API
errors or a mismatched existing ref block the upload/transition; after the public
transition, the exact tag ref must exist and resolve to the candidate SHA.

## Intentional alpha.5 publication

Before publication, PR and main-source checks packaged alpha.5 while `VERSION`
remained alpha.4. The publisher required matching versions, so those previews
did not publish. The alpha.5 contract requires schema 8; alpha.3 and alpha.4
retain their historical schemas 6 and 7. Archives with another schema are rejected.
PR previews always disable publication. Dispatch rehearsals require the exact
current main SHA and `publish=false` for preparation. The owner authorized
[PR #90](https://github.com/Lephiziel/findrail/pull/90), which deliberately changed
`VERSION` to alpha.5. All gates ran again on its actual main merge commit before
publication. Later main commits validate the immutable public release and return
`noop`; they must not replace its tag or assets. The migration checks pin the
published alpha.4 Linux amd64 archive to SHA-256
`41f91c62b618a64dc083f7a63403f7f4fcc6f8b13ffb613acfee6704927f6a1a`; mismatch
fails closed rather than updating the reference silently.

The [alpha.5 publication record](release-alpha5-verification.md) records its
release commit, workflow and downloaded asset bytes. The earlier
[candidate acceptance procedure](release-smoke-alpha5.md#publication-after-acceptance)
remains a historical preparation record. A future source schema needs a new
explicit candidate/version contract, not an edit to an immutable release contract.
