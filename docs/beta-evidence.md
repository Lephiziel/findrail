# Beta.1 evidence handoff contract

This is the offline consumer contract for TASK 33 and the publisher contract
used by TASK 34. It reuses existing producers and validators; it does not define
a second readiness engine. `scripts/beta_evidence.py` is offline. The separate
`scripts/beta_evidence_github.py` is used only by the explicitly dispatched,
read-only workflow to verify GitHub API metadata and retrieve exact artifact IDs.
The offline helper verifies local bytes and internal consistency; it cannot
authenticate hand-written run metadata supplied outside that workflow.

## Immutable identity and trusted provenance

Every handoff is for one exact candidate and records:

- repository `Lephiziel/findrail`;
- a lowercase, exact 40-character candidate commit SHA;
- candidate version `0.1.0-beta.1`, schema `8`, Client API `1`, VSIX `0.1.0`;
- the canonical successful `Alpha release candidate` workflow run ID, attempt,
  event/branch/head SHA, and every consumed candidate artifact's exact name,
  artifact ID, and GitHub artifact digest;
- the successful `Explicit performance check` run ID and attempt for the same
  SHA/version with `candidate_version=0.1.0-beta.1`, the pinned alpha.5 baseline
  `cb53ee9638f69b47b386921e41368f3b66ceddde`, `suite=all`, and
  `duration_seconds=4500`, plus the IDs and digests of both its
  `performance-series-<sha>-<run-id>` and
  `performance-soak-<sha>-<run-id>` artifacts;
- an exact inventory of all eight release assets: basename, positive byte
  size, and SHA-256, plus explicit SHA-256 values for `release-manifest.json`
  and `SHA256SUMS.txt`;
- the extracted Linux amd64 CLI SHA-256, identical in the strict summary,
  recovery, both scale measurements, every candidate series sample/report, and
  soak report;
- the technical outcome list and the original post-scale readiness JSON,
  including each human category still pending.

The workflow verifies GitHub metadata before downloading: canonical repository,
the exact workflow file identity, requested run ID, exact head SHA, main branch,
allowed event, successful conclusion and current run attempt, and the complete
required job set. It rejects skipped jobs except the candidate publisher. The
GitHub Actions API does not expose `workflow_dispatch` inputs on a run record;
the performance run is therefore bound by its same-SHA dispatch event, both
successful series/soak jobs and exact artifacts, and the full artifact contents:
beta.1 candidate identity, pinned baseline, 2700-second series configuration,
and a clean soak of at least 1800 seconds. In the pinned performance workflow,
both jobs can run together only with `suite=all` and `duration_seconds=4500`.

Artifacts are resolved by the explicit run and artifact IDs, never by “latest
successful” name matching. The API digest is checked against the downloaded
artifact ZIP before safe extraction. Both GitHub artifact digests and local
payload-file hashes are retained. A PR/fork artifact, expired/missing artifact,
failed or stale attempt, ambiguous name, or record for another SHA/version is
not candidate proof. Artifact IDs and run IDs are decimal strings in JSON to
avoid numeric precision loss.

The canonical eight-file set is the one verified by
`scripts/release_assets.py`; the five application archives are obtained from
the canonical `release-assets-<sha>` artifact and remain separate from the
reports handoff. Do not put app archives in the reports ZIP or turn the ZIP into
a ninth release asset. `SHA256SUMS.txt` covers the seven other release assets,
not itself; its own digest is recorded in the inventory.

## Workflow and helper interface

Run `.github/workflows/beta-evidence.yml` only from canonical `main`, with
explicit candidate and performance run IDs. In `initial` mode,
`candidate_sha` must equal the workflow's own source SHA. In `refresh` mode,
`candidate_sha` stays bound to the original candidate and `refresh_run_id` must
identify a successful prior handoff for that same candidate; the workflow source
SHA may be newer. The workflow grants only `contents: read` and
`actions: read`; the token is present only in the metadata/download step. It
does not build or release the app, create tags, write repository contents, or
modify `VERSION`. Initial mode downloads by artifact ID, extracts the checked
Linux amd64 archive, and measures only that binary. Refresh mode verifies and
reuses the prior reports-only handoff and source artifacts; it does not extract
or rebuild the app or rerun scale, series, or soak measurements. Pull requests
receive the synthetic CI tests, not a path to submit evidence to this workflow.

The offline `scripts/beta_evidence.py` commands have stable subcommands and
option names. Paths are explicit; the helper does not discover or build an
application binary and does not fetch trusted metadata. For example:

```sh
python3 scripts/beta_evidence.py assemble \
  --candidate-sha "$CANDIDATE_SHA" \
  --candidate-version 0.1.0-beta.1 \
  --assets-dir /abs/evidence/release-assets \
  --verification-summary /abs/evidence/main-verification/verification-summary.json \
  --recovery /abs/evidence/linux-package/recovery-candidate.json \
  --performance-series-dir /abs/evidence/performance-series \
  --performance-soak /abs/evidence/performance-soak/report.json \
  --scale-10k /abs/evidence/scale-10k.json \
  --scale-100k /abs/evidence/scale-100k.json \
  --run-metadata /abs/evidence/trusted-run-metadata.json \
  --kits-dir /abs/evidence/kits \
  --output-dir /abs/evidence/handoff \
  --reports-zip /abs/evidence/beta-evidence-reports.zip

python3 scripts/beta_evidence.py verify \
  --candidate-sha "$CANDIDATE_SHA" \
  --candidate-version 0.1.0-beta.1 \
  --assets-dir /abs/evidence/release-assets \
  --handoff-dir /abs/evidence/handoff \
  --reports-zip /abs/evidence/beta-evidence-reports.zip

python3 scripts/beta_evidence.py refresh \
  --candidate-sha "$CANDIDATE_SHA" --candidate-version 0.1.0-beta.1 \
  --previous-bundle-dir /abs/evidence/prior-workflow-artifact \
  --source-artifacts-dir /abs/evidence/source-artifacts \
  --assets-dir /abs/evidence/release-assets \
  --run-metadata /abs/evidence/trusted-refresh-run-metadata.json \
  --windows-report /abs/evidence/windows-report.json \
  --output-dir /abs/evidence/refreshed-handoff \
  --reports-zip /abs/evidence/refreshed-beta-evidence-reports.zip
```

`--run-metadata` is a bounded JSON file produced by the workflow after GitHub
API checks; it carries candidate and performance workflow/run-attempt
identities and the consumed artifact IDs/digests. The online preflight can also
be run directly in an authenticated, read-only environment:

```sh
python3 scripts/beta_evidence_github.py preflight \
  --candidate-sha "$CANDIDATE_SHA" --candidate-version 0.1.0-beta.1 \
  --candidate-run-id "$CANDIDATE_RUN_ID" \
  --performance-run-id "$PERFORMANCE_RUN_ID" \
  --download-dir /abs/evidence/downloads \
  --metadata /abs/evidence/trusted-run-metadata.json
```

The preflight requires a read-only `GITHUB_TOKEN`, validates all requested run
and job metadata before transfer, bounds artifact downloads/extraction, and
downloads the five required artifact payloads by their verified IDs. It does
not query “latest” runs. Refresh preflight additionally downloads the exact
previous handoff artifact by its run/artifact ID and verifies its GitHub digest.
It still verifies the candidate/performance run IDs and source-artifact digests.
For refresh, add `--previous-handoff-run-id "$REFRESH_RUN_ID"`; preflight
extracts that artifact under `previous-handoff/` and records its run/artifact
identity in the trusted metadata file.
`assemble` verifies the exact assets with
`scripts/release_assets.py verify --directory DIR --version 0.1.0-beta.1
--revision SHA`, reuses `scripts/candidate_record.py` and `scripts/readiness.py`
contracts, and records but does not rewrite producer reports. It extracts the
Linux CLI only through the existing safe package extractor; it never substitutes
a local rebuild. The workflow then runs `search_resource_check.py` against that
extracted CLI for 10k and 100k documents and performs an independent offline
`verify` before upload.

`verify` prints one fixed JSON result shape to stdout on both success and
failure: `{"result_version":1,"status":"passed|failed","candidate_sha":"…","candidate_version":"0.1.0-beta.1","report_zip_sha256":"…|null"}`.
It returns zero only for `passed`; invalid, incomplete, mismatched, or failed
inputs return nonzero. Diagnostics must not contain report prose, private
paths, indexed text, queries, or raw logs. The report ZIP digest is computed
after ZIP finalization and is emitted in this result and the trusted workflow's
artifact metadata/job summary, never embedded in the ZIP itself.
`--reports-zip` is a sibling output/input, never inside `--output-dir`; it must
also be outside `--assets-dir`. The ZIP contains the handoff payload and is
verified against its manifest. `assemble` refuses to overwrite either output;
use new paths to preserve existing evidence until the replacement is verified.
The workflow uploads a bounded result/diagnostic
artifact even on job failure; successful artifacts have 30-day retention. The
job summary records the handoff artifact link/digest, report ZIP digest,
PACKAGE-MANIFEST digest, source run/artifact IDs, candidate binary hash, and
human gaps. Preserve the report ZIP and both inner hashes before the retention
period expires.

## Handoff layout

Keep the established TASK 30 paths and leave producer JSON/sidecars unchanged.
The TASK 33 handoff uses this layout (the two evidence wrappers add only the
existing readiness `kind` and `measurement` envelope):

```text
README.md
evidence-index.json
PACKAGE-MANIFEST.json
publication-notes.md
main-verification/verification-summary.json
linux-package/recovery-candidate.json
evidence/recovery.json
performance-series/                 # provenance, series.json, raw reports and sample sidecars
performance-soak/report.json        # plus provenance.txt
scale-10k.json
evidence/scale-10k.json             # kind=scale_10k; exact raw measurement under measurement
scale-100k.json
evidence/scale-100k.json            # kind=scale_100k; exact raw measurement under measurement
readiness-after-scale.json
kits/linux-amd64.zip
kits/windows-amd64.zip
kits/darwin-arm64.zip
```

`evidence/recovery.json` uses `kind: "crash_recovery"`; its `measurement` is
the unchanged `linux-package/recovery-candidate.json`. The scale wrappers use
the existing `readiness.py --evidence FILE` object format. The strict summary
and recovery retain their original locations and producer schemas. The
canonical app archives are referenced by inventory, not copied into this
reports-only handoff.

The strict summary is the `candidate_record.py summary` result from the
canonical release run; its `candidate_sha`, `candidate_version`, schema/API,
native target records, eight asset records and journey must all match the exact
assets and candidate. `linux-package/recovery-candidate.json` must be a passed
Linux recovery record for that SHA/schema. Both scale files are the unchanged
`search_resource_check.py` reports on the extracted candidate binary, fixture
v2/seed 42, 10,000 and 100,000 documents, with source-cycle boundaries (and
`--allow-large` for 100k). Verify all producer checks, not just the top-level
status: corpus/counts, paging 20/50/100, refresh/cancellation/concurrent reads,
evidence boundaries, and exact binary hash.

The performance-series artifact must retain the full TASK 21 series (10k and
100k, three repeats, 100 samples per family, candidate and baseline); preserve
its `reference_only` qualification and environment differences. The soak
artifact must be a clean, complete TASK 23 long run of at least 1800 seconds,
with `source_dirty=false` and no failures. The candidate identity inside the
series and soak must match the strict Linux executable hash. A baseline hash is
separately identified and must never be represented as the candidate.

The original `readiness-after-scale.json` is generated by the existing
`scripts/readiness.py` from the strict summary, recovery, and actual scale
wrappers. With no matching human reports, expected human gaps are exactly
`windows_first_run`, `apple_silicon_first_run`, and `voluntary_usage`; technical
evidence must not be pending, failed, invalid, or mismatched. Keep
`final_release: false`. Empty kits remain `not_run` and are not passed to the
classifier. A previous TASK 30 package rehearsal may be recorded only as an
additional rehearsal for this same SHA; it cannot replace the strict candidate
run or fill an absent artifact from an older SHA.

## Index, package manifest, and publication notes

`evidence-index.json` identifies the repository/candidate/version/schema/API,
canonical candidate and performance runs/attempts, consumed artifact
names/IDs/GitHub digests, all eight asset basenames/sizes/SHA-256 values,
manifest and checksum hashes, the shared Linux binary hash, technical outcomes,
readiness status and human pending categories. It also records the exact
`publication-notes.md` SHA-256. Technical outcomes must retain
failure/invalid states; do not summarize a partial run as passed.

`PACKAGE-MANIFEST.json` lists the relative path, byte size and SHA-256 of every
payload file in the handoff, excluding `PACKAGE-MANIFEST.json` itself. The
reports ZIP contains that manifest and the handoff payload; calculate its final
digest only after the ZIP is closed. Store that outer digest in trusted artifact
metadata and the job summary, not as a member of the ZIP or its own manifest.
Store the digest of `PACKAGE-MANIFEST.json` in trusted artifact metadata/job
summary and the external owner card, not inside `evidence-index.json` (which is
itself covered by the package manifest); this avoids a circular hash dependency.
Do not include indexes, browser profiles, credentials, raw private reports,
private paths, document text, queries, or outreach drafts.

TASK 35/A freezes the static [`release-notes-beta1.md`](release-notes-beta1.md)
before the candidate freeze. TASK 33 copies its exact UTF-8 bytes into
`publication-notes.md` and records SHA-256 over those exact bytes. TASK 36/A
shows that exact body/hash to the owner; TASK 34 must compare the approved hash
and body before publication. If later matching human evidence changes the
publication notes, preserve the technical files/run IDs/hashes, rebuild the
outer handoff/readiness and ZIP digest, and show the changed notes/hash and
outer digest to the owner again. Do not rerun scale, series, or soak merely to
update human intake.

## Optional human-report refresh

The explicit TASK 33 workflow may accept optional sanitized Windows and Linux
report JSON inputs after real observations. Its input names are
`windows_report_json` and `linux_report_json`; the local helper options are
`--windows-report FILE` and `--linux-report FILE`. Each is capped at 8 KiB
before parsing and rejects duplicate JSON keys, non-finite values, and unknown
or unsafe content. The Windows report uses the existing `windows_first_run`
kind and readiness/manual-evidence validator. The Linux report is the separate
TASK 32 observation kind and does not become a new readiness gate or substitute
for a current human category. No measurement is repeated when a report is
accepted.

Workflow-dispatch inputs are visible to repository maintainers in the run
record. Submit only sanitized data; do not include personal identifiers,
private paths, document text, query terms, credentials, raw logs, or confidential
free-form notes. The helper never prints or uploads the original report JSON.
The handoff keeps only candidate/archive identity, a validated public reference,
ordered case outcomes and elapsed times. It does not retain free-form observer,
environment or obstacle prose. If that evidence scope is insufficient, do not
submit the report through this workflow.

The workflow parser reads bounded fields directly from `GITHUB_EVENT_PATH`;
raw JSON is not placed in a shell-step environment or interpolated into shell
source. Only temporary file paths are passed to later steps, and the original
input report is not uploaded. A JSON record does not establish identity: the
operator confirms that it came from a real voluntary observation.
Refresh requires a successful prior handoff run ID. Preflight checks the exact
candidate, performance, prior handoff run identities and artifact IDs/digests;
the offline refresh verifies the prior result, diagnostics, reports ZIP,
package manifest, candidate-bound source files, and technical producer reports.
It changes only human intake, readiness, notes, index, package manifest, and
outer ZIP. It checks that technical measurement/package files retain their
original byte hashes and that candidate/performance run identities are
unchanged. The index records the immediately previous handoff run and artifact
ID/digest as refresh provenance. The workflow independently verifies the
refreshed handoff before upload. The updated outer manifest/ZIP fingerprint
changes and must be reviewed again. A matching passed
Windows report can change only `windows_first_run` from pending to passed;
Apple Silicon and voluntary-use gaps remain pending unless their own matching
reports are supplied. Failed, invalid, or mismatched reports remain visible and
block acceptance; they are never discarded as optional.

## Ownership and boundaries

- TASK 32 owns kits and manual intake formats.
- TASK 33 owns this evidence helper, read-only workflow, and evidence assembly.
- TASK 34 owns the explicit publisher and byte-promotion guard.

This contract does not authorize a release, create evidence for a future SHA,
make private reports public, change `VERSION`, or expand the limited beta into
stable/v1.
