# Release readiness

This policy separates shipped software, source-candidate evidence, and product
release decisions. Per-candidate `release-readiness-<SHA>` CI artifacts contain
the actual status; this page does not certify a future commit. Green automation
is not user evidence. The classifier cannot set `final_release=true` or authorize
publication. The canonical cross-platform promises and the narrower limited-beta
owner exception are in [beta support](beta-support.md); the evidence packet
contract is in [beta evidence](beta-evidence.md).

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
| Strict software candidate | Exact SHA/version; required root/transfer/common browser cases; five native targets, installed VSIX and exact asset verification | Existing strict `candidate_record.py summary`, after the mandatory candidate jobs |
| Native install lifecycle | Five real targets, eight fixed installer cases each, matching archive and desktop/CLI hashes | Validated native records from that summary |
| Freshness and first run | Two additional fixed freshness browser cases; passage-window smoke also executes the keyboard first-run journey | Existing packaged browser reports on the supplied extracted candidate |
| Crash/recovery | Actual Linux SIGKILL, reopened integrity checks, complete old-or-new state | `recovery_candidate.py` on the same extracted production binary; a required technical outcome for this handoff |
| Final 10k / 100k | Complete fixture-v2/seed-42 runs on the final candidate, paging 20/50/100 and refresh/cancellation | Required on the final frozen beta candidate; old TASK17/alpha runs do not certify it |
| Manual Windows first run | Matching human report on the exact `windows-amd64` archive | Existing `windows_first_run`; the planned Windows VM run remains pending until a matching passed report is validated; failed/invalid reports block |
| Manual Apple Silicon first run | Matching human report on the exact `darwin-arm64` archive | Existing `apple_silicon_first_run`; native CI is not Finder/Gatekeeper evidence |
| Ordinary use and return | Voluntary human retrieval, collection and later return-use reports | Existing `voluntary_usage`; CI and empty kits cannot substitute |
| Language review / release decision | Human review and owner decision | Remains with the owner |

The normal CI readiness invocation may leave scale and human evidence pending.
That does not waive the final candidate's required technical gates: before the
limited-beta owner decision, strict software acceptance, all five native
targets, browser/VSIX/asset verification, recovery, and 10k/100k scale evidence
must pass on the matching candidate. Exact-head Go/vet/race, Python, UI,
upgrade/restore, integrity and other required workflow checks remain mandatory.
Readiness does not replace those gates.

## Validation contract

`scripts/readiness.py` revalidates the retained strict summary, fixed case sets,
native target inventory, candidate SHA/version, schema/API versions and related
archive/binary hashes. It consumes the existing producer records; reports cannot
choose their own mandatory cases. The workflow first verifies the actual assets
with `release_assets.py` and `candidate_record.py`, then passes their summary to
the classifier. The classifier is not a substitute for that byte verification.

Missing evidence is `pending`. Supplied malformed, duplicate, failed, skipped,
interrupted, inconsistent or unmeasured evidence is `blocked` and gives a
nonzero CLI exit status. `software_status`, `release_readiness`, and the owner
decision are distinct: software status does not make invalid scale or human
evidence disappear; readiness completeness does not authorize a release; and
the owner decision does not rewrite either status. Every output keeps
`final_release=false`, including `eligible-for-owner-decision`.

For a limited desktop beta only, the owner may consider unresolved
`windows_first_run`, `apple_silicon_first_run`, and `voluntary_usage` after all
technical evidence and mandatory CI pass. They remain pending in the JSON; this
exception is not encoded as a classifier pass. Pending scale/recovery, failed,
invalid, mismatch, or unsuccessful required CI is never covered by this scope.
Zero independent users permits preparing a decision card but does not prove
usefulness, retention, or full beta/v1 readiness. See the complete
[platform and stage policy](beta-support.md).

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

## Freeze and next decision

TASK 34 implements the explicit-publisher protection and must be integrated
before TASK 35/A changes `VERSION`. Once integrated, a main push stays read-only
even when the candidate and `VERSION` match; publication requires a separate
owner-only canonical-main dispatch with the exact accepted handoff, run attempts,
and reviewed fingerprints. Freeze one exact main SHA, run required candidate CI
and final technical evidence on it, then present the handoff, remaining human
pending categories, asset hashes, limitations, and release-note body/hash to the
owner. If matching human reports arrive later, refresh intake/readiness and the
outer handoff fingerprint without repeating measurements; show the changed
fingerprint again.

Only a separate explicit owner decision followed by the guarded publisher can
change the public download. Alpha.5 remains current until beta.1 is actually
published and its public bytes are independently verified. This process is not
a broad/stable/v1 release path. A completed code PR may merge while release
evidence remains pending; this policy leaves VERSION, tags, and historical
releases unchanged.
