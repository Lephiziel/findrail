# Beta.1 evidence handoff contract

This is the offline consumer contract for TASK 33 and the publisher contract
used by TASK 34. It reuses existing producers and validators; it does not define
a second readiness engine or make any GitHub request itself. The read-only
workflow is responsible for trusted GitHub run/artifact metadata and downloads.
The helper verifies local bytes and their internal consistency, not the
authenticity of a hand-written run URL or JSON file.

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

The workflow must verify GitHub metadata before trusting downloads: canonical
repository, allowed workflow identity, exact head SHA, main event/branch,
successful conclusion and current run attempt, and required jobs/artifacts.
Resolve artifacts by the explicit run and artifact IDs, not by “latest
successful” name matching. Record both the GitHub artifact digest and local
payload-file hashes. A PR/fork artifact, expired/missing artifact, failed
attempt, ambiguous name, or record for another SHA/version is not a candidate
proof. Artifact IDs and run IDs should be represented as decimal strings in
JSON to avoid numeric precision loss.

The canonical eight-file set is the one verified by
`scripts/release_assets.py`; the five application archives are obtained from
the canonical `release-assets-<sha>` artifact and remain separate from the
reports handoff. Do not put app archives in the reports ZIP or turn the ZIP into
a ninth release asset. `SHA256SUMS.txt` covers the seven other release assets,
not itself; its own digest is recorded in the inventory.

## Offline helper interface

TASK 33 implements `scripts/beta_evidence.py` with the following stable
subcommands and option names. Paths are explicit; the helper does not discover
an application binary or fetch trusted metadata on its own.

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
```

The options above are the TASK 33 interface, not commands available in this
documentation-only change. `--run-metadata` is a bounded JSON file produced by
the read-only workflow after GitHub API checks; it carries candidate and
performance workflow/run-attempt identities and the consumed artifact
IDs/digests. `assemble` verifies the exact assets with
`scripts/release_assets.py verify --directory DIR --version 0.1.0-beta.1
--revision SHA`, reuses `scripts/candidate_record.py` and `scripts/readiness.py`
contracts, and records but does not rewrite producer reports. It extracts the
Linux CLI only through the existing safe package extractor; it never substitutes
a local rebuild.

`verify` prints one fixed JSON result shape to stdout on both success and
failure: `{"result_version":1,"status":"passed|failed","candidate_sha":"…","candidate_version":"0.1.0-beta.1","report_zip_sha256":"…|null"}`.
It returns zero only for `passed`; invalid, incomplete, mismatched, or failed
inputs return nonzero. Diagnostics must not contain report prose, private
paths, indexed text, queries, or raw logs. The report ZIP digest is computed
after ZIP finalization and is emitted in this result and the trusted workflow's
artifact metadata/job summary, never embedded in the ZIP itself.
`--reports-zip` is a sibling output/input, never inside `--output-dir`; it must
also be outside `--assets-dir`. The ZIP contains the handoff payload and is
verified against its manifest.

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
report JSON inputs after real observations. The helper option names are
`--windows-report FILE` and `--linux-report FILE`; each file is capped at 8 KiB
before parsing and rejects duplicate JSON keys, non-finite values, and unknown
or unsafe content. Use the existing `windows_first_run` kind and the existing
readiness/manual-evidence validator. Store VM/build/browser/download details in
the already bounded `REPORT.md`/notes fields; do not introduce a new Windows
readiness kind. Linux first-run is the separate TASK 32 observation kind and
does not become a new required readiness gate or a substitute for any current
human category.

If workflow-dispatch inputs are used, pass bounded sanitized JSON as a
structured input or file and have a dedicated parser write/read it as data.
Do not interpolate JSON into shell source, log it, or upload the original
private report without the observer's consent. A JSON record does not establish
identity: the operator confirms that it came from a real voluntary observation.
With no new technical run, matching reports update intake/readiness only;
technical bundle/source run IDs and hashes stay identical. The updated outer
manifest/ZIP fingerprint changes and must be reviewed again. A matching passed
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
