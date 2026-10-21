# Release readiness

This policy separates shipped software, source-candidate evidence, and product
release decisions. Per-candidate `release-readiness-<SHA>` CI artifacts contain
the actual status; this page does not certify a future commit. Green automation
is not user evidence. The classifier cannot set `final_release=true` or authorize
publication.

## Fixed v1 scope

Selected local folders → lexical passage search → source provenance and
freshness → quotes and saved collections → safe upgrade and rollback.
OCR, semantic search, private GitHub/cloud/team/authentication, new connectors,
and automatic updates are outside v1 scope. Public GitHub snapshots, MCP and the
VSIX are existing optional surfaces; regressions matter, but they do not create
a new local-v1 SDK-contributor gate.

## Evidence and current gaps

| Gate | Evidence | How status is obtained |
|---|---|---|
| Strict software candidate | Exact SHA/version; all 19 root, 20 transfer and 7 original common browser cases; four resource records | Existing strict `candidate_record.py summary`, after native, browser and installed-VSIX jobs |
| Native install lifecycle | Five real targets, eight fixed installer cases each, matching archive and desktop/CLI hashes | Validated native records from that summary |
| Freshness and first run | Two additional fixed freshness browser cases; passage-window smoke also executes the keyboard first-run journey | Existing packaged browser reports on the supplied extracted candidate |
| Crash/recovery | Actual Linux SIGKILL, reopened integrity checks, complete old-or-new state | `recovery_candidate.py` on the same extracted production binary; required by the alpha workflow |
| Final 10k / 100k | Complete fixture-v2/seed-42 runs on the final candidate, paging 20/50/100 and refresh/cancellation | Pending until supplied; historical TASK17 measurements do not certify the final candidate |
| Manual Windows and Apple Silicon first run | Human observations on the matching archives | No human reports recorded by this change; automated native jobs cannot substitute |
| Ordinary use and return | Voluntary human retrieval, collection and later return-use reports | No reports recorded by this change; CI cannot substitute |
| Language review / release decision | Human review and owner decision | Remains with the owner |

The normal CI readiness invocation leaves scale and human evidence pending.
It can pass software acceptance without claiming beta/v1 readiness. Required
exact-head Go/vet/race, Python, UI, five native targets, packaged browser, MCP
configuration, installed VSIX, upgrade/restore, integrity and final asset checks
remain mandatory in the existing workflow. Readiness does not replace them.

## Validation contract

`scripts/readiness.py` revalidates the retained strict summary, fixed case sets,
native target inventory, candidate SHA/version, schema/API versions and related
archive/binary hashes. It consumes the existing producer records; reports cannot
choose their own mandatory cases. The workflow first verifies the actual assets
with `release_assets.py` and `candidate_record.py`, then passes their summary to
the classifier. The classifier is not a substitute for that byte verification.

Missing optional scale or human evidence is `pending`. Supplied malformed,
duplicate, failed, skipped, interrupted, inconsistent or unmeasured evidence is
`blocked` and gives a nonzero CLI exit status. Software status is separate:
invalid scale or human evidence cannot erase a valid software result. Every
output keeps `final_release=false`, including `eligible-for-owner-decision`.
Eligibility describes evidence completeness, not a release authorization or a
count of satisfied users. The owner still reviews reported problems and quality.

Input JSON is limited to 1 MiB per file and rejects duplicate keys and nonfinite
numbers. Only fixed status codes and candidate provenance are emitted. Indexes,
browser profiles, raw logs, private paths, document text and queries are not
retained in the readiness artifact.

The alpha workflow supplies the strict summary and raw recovery report:

```bash
python3 scripts/readiness.py --candidate verification-summary.json \
  --candidate-sha "$CANDIDATE_SHA" --candidate-version 0.1.0-alpha.5 \
  --recovery recovery-candidate.json --output release-readiness.json
```

Additional records are passed with repeated `--evidence FILE` arguments:

- Scale: an object with `kind` equal to `scale_10k` or `scale_100k` and
  `measurement` containing the unchanged `search_resource_check.py` JSON.
  Use the extracted candidate, `--documents 10000` / `100000`, `--seed 42`,
  `--source-cycle-boundaries`, and `--allow-large` for 100k. Limits remain
  unchanged. The validator checks the fixed corpus hashes, exact candidate
  binary, complete document counts, bounded paging and refresh results. If the
  measurement contains a comparison, set `baseline_role` to `reference`; that
  explicitly permits the reference binary to have a different SHA/hash.
- Manual OS: `kind` is `windows_first_run` or `apple_silicon_first_run`;
  `evidence_type` is `manual`, `status` is `passed`, and `candidate_sha`,
  `candidate_version`, `target`, and `archive_sha256` match the tested archive.
  `cases` contains the fixed names `install`, `launch`, `repeat-launch`,
  `retrieval`, `collection-restart`, `refresh`, `quit`,
  `uninstall-data-preserved`, each with `status` and finite nonnegative
  `elapsed_seconds`. `reference` is the voluntarily submitted project issue URL
  or `sha256:<digest>` of a privately retained report; public submission is not
  required. Record observed security prompts honestly; do not bypass them.
- Usage: `kind` is `voluntary_usage`, with matching candidate SHA/version and a
  bounded `reports` list. Each report has a distinct `reference` as above,
  `evidence_type: manual`, `return_use: true`, `retrieval_one` and `retrieval_two`
  (`found` / `not_found`), and `collection_restart` (`restored` /
  `not_restored`). Misses remain visible for owner review; no private query is
  required. Automated reports and placeholders cannot satisfy this evidence.

For a beta.1 candidate, both performance workflow inputs and classifier
provenance must name `0.1.0-beta.1`. The workflow builds its candidate binary
through `scripts/package.py --version 0.1.0-beta.1` and compares it with the
separately packaged pinned alpha.5 baseline. Confirm that the series/soak
candidate binary SHA-256 equals the Linux archive's recorded executable hash
before passing scale measurements to readiness. Older alpha.5-only performance
runs are historical, not beta evidence.

## Next decision

Freeze a candidate after software checks, perform fresh 10k/100k runs on its
archives, and collect the relevant manual platform reports. Then review
voluntary ordinary use and remaining defects. The five automated native jobs
are not Finder, SmartScreen or assistive-technology observations. Full public
beta needs the relevant manual results or a separately documented owner decision
to limit platform support.

Mid-December 2026 is a planning target, not a promised date; indexing or migration
issues can move it. A completed code PR may merge while release evidence remains
pending. Publishing alpha.5, beta or v1 requires a separate explicit owner
decision; this change leaves VERSION, tags and historical releases unchanged.
