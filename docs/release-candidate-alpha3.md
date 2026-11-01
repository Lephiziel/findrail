# alpha.3 release-candidate acceptance (not a release)

This retains the dated candidate evidence. Alpha.3 is now published; see
[public release verification](release-alpha3-verification.md) for its final SHA,
downloads and checks.

Candidate acceptance evidence recorded 2026-10-13. This was a technical
**conditional GO** for the alpha.3 candidate, not permission to publish. At that
checkpoint the owner's short human smoke and approval of release notes/target
were still before-release actions.
At that acceptance checkpoint the published release was alpha.2 and no alpha.3
tag or release existed. See the dated owner handoff below for the later decision.

## Candidate identity

| Field | Verified value |
|---|---|
| Candidate `main` SHA | `0254e3063182695b2742f6c6d166024123c9d0f7` |
| Candidate tree SHA | `4033433f7d9bfd6cb7b8328689c24ba9ab3d435f` |
| Clean checkout | Yes; detached checkout at the SHA, no tracked changes or untracked payload |
| App candidate version | `0.1.0-alpha.3` (explicit no-publish workflow override) |
| Repository `build/package/VERSION` | `0.1.0-alpha.2` (unchanged; not the candidate override) |
| Extension version | `0.1.0` |
| Client API / schema | `1` / `6` |
| Release state | Only alpha.1 and alpha.2 published; alpha.3 tag/release absent |

PRs #30, #31 and #32 are merged. The candidate was rebuilt from the exact
post-integration `main` SHA by the [no-publish workflow dispatch](https://github.com/Lephiziel/findrail/actions/runs/38342270683)
with `candidate_version=0.1.0-alpha.3` and `publish=false`. All required jobs
passed; `release-state` and `publish` were skipped. The earlier automatic `main`
push rehearsal used the unchanged VERSION (`0.1.0-alpha.2`); it is not used as
alpha.3 evidence.

## Verified assets

The [aggregate release artifact](https://github.com/Lephiziel/findrail/actions/runs/38342270683/artifacts/11787422558)
contains the exact eight-file set below. The downloaded local copy was verified
with `scripts/release_assets.py verify --version 0.1.0-alpha.3 --revision
0254e3063182695b2742f6c6d166024123c9d0f7` and `sha256sum -c SHA256SUMS.txt`.
All checks passed. Artifacts are unsigned; no notarization or signing is claimed.

| Asset basename | Target | Size (bytes) | SHA-256 | Source revision | Native/verification job |
|---|---|---:|---|---|---|
| `findrail_0.1.0-alpha.3_linux-amd64.tar.gz` | Linux amd64 | 6,271,322 | `f805444cfbc68cd03e104b7b5d6c947609c3f96022a5dfc25f94cce5b5b361b8` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [Linux amd64](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115115725051) |
| `findrail_0.1.0-alpha.3_linux-arm64.tar.gz` | Linux ARM64 | 5,794,204 | `280d77039685c92e552a5386c3b4b084680b2b7b9b1d3726c828c1209176d819` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [Linux ARM64](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115115725023) |
| `findrail_0.1.0-alpha.3_darwin-amd64.tar.gz` | macOS Intel | 6,284,764 | `40eed86d175e5a36e597b90f0089a958e55b9353a7db59da27b9d0d694544bd7` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [macOS Intel](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115115725077) |
| `findrail_0.1.0-alpha.3_darwin-arm64.tar.gz` | macOS ARM64 | 5,928,012 | `6219afb4f6cb076e5d34931f6866712afa37bbd86c5f15bfe80907ec1d4dd3a2` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [macOS ARM64](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115115725035) |
| `findrail_0.1.0-alpha.3_windows-amd64.zip` | Windows amd64 | 15,526,399 | `f6c7f9d35f658b6110737c69adf7fcd0e7d937c8d30f6bd32fd4b54afe23312e` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [Windows](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115115725140) |
| `findrail-readonly-0.1.0.vsix` | VS Code UI extension | 18,033 | `5845c1cb52197676065c40b3e8e7830241b9b9d1f10e10e18edaf5eaec117094` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [Installed VSIX + candidate server](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115116096046) |
| `release-manifest.json` | Manifest v1 | 1,612 | `20824288ed11ee08531e10a7f3f7989d77f0f6b906b391ec51dbaad087c8066f` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [Aggregate verification](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115116390924) |
| `SHA256SUMS.txt` | Shared checksums | 724 | `cd1a0a0301b63fd4ded3cd6b06faa46179ae0e1066ff987bb2257ca2c816ad14` | `0254e3063182695b2742f6c6d166024123c9d0f7` | [Aggregate verification](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115116390924) |

Manifest metadata: `manifest_version=1`, app `0.1.0-alpha.3`, extension `0.1.0`,
API 1, schema 6, source revision above. The manifest covers six installable
files; `SHA256SUMS.txt` covers those six plus the manifest, and does not hash
itself. No asset in this table is a public release URL. Download the aggregate
artifact from the workflow link above; it is a temporary CI artifact, not a
published release.

## Checks and platform boundaries

| Case / OS | Status | Evidence |
|---|---|---|
| Go formatting, full tests, vet, CGO-free build, module verification; Linux/macOS/Windows | passed | [Candidate workflow](https://github.com/Lephiziel/findrail/actions/runs/38342270683); source-check matrix jobs |
| UI Node tests, packaging safety, release-asset contract tests | passed | `source-checks / ui` in candidate workflow |
| Race tests | passed | `source-checks / race` in candidate workflow |
| Native package extraction, BUILD-INFO exact SHA/clean/version/target, CLI/search/MCP/portable/diagnostics; all five targets | passed | Five native jobs in the asset table; target results are not transferred across OSes |
| Evaluation, ignore policy, source report/failed-scan retention and schema-2 → 6 backup/rollback on extracted Linux amd64 candidate | passed | [Packaged browser journey report](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115116096070), plus Linux native job |
| Empty start, browser Add folder, evidence selection, Markdown bytes, restart | passed | [Real Chromium journey on extracted Linux archive](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115116096070) |
| Installed VSIX against extracted candidate server, API v1 | passed | [Isolated installed-VSIX job](https://github.com/Lephiziel/findrail/actions/runs/38342270683/job/115116096046) |
| VSIX package/unit checks on Linux, Windows, macOS | passed | Installed candidate VSIX host test on Linux in this run; [PR #30 editor matrix](https://github.com/Lephiziel/findrail/actions/runs/38336454628) passed on all three OSes. Its `extensions/vscode` tree SHA (`9297d64f1aec5fecfdaa22acf905cbb95b6c72a4`) is unchanged at this candidate SHA; platform GUI installation is not inferred |
| Finder/Gatekeeper behavior on macOS; Windows manual UI | not run | Not a release gate and not implied by native package CI |
| Owner-run human smoke on one computer | pending | [10–15 minute smoke sheet](release-smoke-alpha3.md) |
| Publication/tag/public URLs | not run | Explicitly outside this acceptance task |

Candidate journey assertions include the synthetic 10/10 evaluation path set,
negative mismatch behavior, missing-index noncreation, legacy-index read-only
rejection, >100-match incomplete result, search/MCP/portable/diagnostics/ignore,
source removal and failed-scan report retention, frozen archive, PDF page 0 and
positive pages, DOCX without page claims, and schema 2 → 6 with stopped-directory
backup/restore. Evaluation remains a path-set check, not a ranking/relevance
score. Browser checks were Linux only. Windows/macOS native jobs do not certify
browser behavior or OS security prompts.

## Documentation and release boundary review

Reviewed the Linux candidate archive’s packaged `INSTALL.txt`, repository README installation examples, editor
setup (`docs/vscode.md`), schema/upgrade/rollback guidance, [prepared publisher
notes](release-notes-alpha3.md), and [promotion checklist](release-alpha3-promotion.md).
The docs distinguish alpha.2 downloads from the alpha.3 candidate, state schema 6
and forward-only migration/whole-directory rollback, identify the separate
server-dependent API v1 VSIX, and make unsigned/no-notarization limits explicit.
No future release download links are asserted. The first-time path is archive →
checksum → extract → empty start → Add folder → preview/citations → Markdown →
restart; no source build, MCP, GitHub or VS Code setup is required.

## Pending human smoke and decision

**Conditional GO for technical readiness; not publication authorization.** The
owner has not supplied results for a personal smoke, and none are fabricated. Run
`docs/release-smoke-alpha3.md` once on one accessible computer (10–15 minutes):
verify the candidate checksum, start with a dedicated empty data directory, Add
folder, inspect a text/PDF citation, export two passages with a note, restart and
confirm source persistence/collection reset. Do not send private content, paths
or logs. No five-OS smoke is requested. Before 06, also approve the prepared
release notes and target `0.1.0-alpha.3`.

## Promotion plan for task 06

- Current `VERSION`: `0.1.0-alpha.2`.
- Proposed target/tag: `0.1.0-alpha.3` / `v0.1.0-alpha.3` (proposal, not set).
- Notes to review/approve: [`docs/release-notes-alpha3.md`](release-notes-alpha3.md).
- Expected release asset set: the exact eight basenames in the table above.
- Final VERSION-commit checks: confirm no alpha.3 tag/release already exists; make
  the authorized minimal VERSION/notes commit; record its new full SHA/tree; rerun
  all native, source, packaged browser/upgrade, installed VSIX and aggregate gates
  on that exact SHA; verify all eight assets and exact provenance; run the
  publisher only after gates and owner authorization.
- Candidate assets above belong to SHA `0254e306…`; they must not be relabeled as
  assets for the later VERSION commit.
- After publication: fetch the actual release record and tag target; download all
  eight assets from the release, verify `SHA256SUMS.txt`, manifest metadata and
  hashes; record real release/download URLs and prerelease/unsigned metadata; only
  then apply the source-to-released documentation promotion checklist.

Earlier PR #30/#31 rehearsals are history only; this acceptance evidence is from
post-integration `main` SHA `0254e3063182695b2742f6c6d166024123c9d0f7`.

## Owner handoff received 2026-10-13

**GO for the authorized alpha.3 release handoff, subject to the final release
commit passing the production gates.** The owner reported following the supplied
startup instructions, adding a folder of Word documents, searching it and opening
a working Preview. The owner then indexed the project repository and confirmed
search worked there too. No private document content or paths are recorded.

This is the actual scope of the owner report. Manual citation export, restart,
PDF-page checking, OS/browser details and an independently reported binary SHA
were not supplied. The earlier candidate's automated Chromium journey covers
citation export, PDF evidence and restart; those results remain automated
evidence and will be repeated on the final release commit. No additional manual
result is inferred.

The owner's existing instruction to execute task 06 authorizes
`0.1.0-alpha.3`; the received local smoke satisfies that handoff's remaining
owner-check condition. The prepared notes and eight-file asset contract were
reviewed and accepted during PR #33 review. `docs/release-notes.md` now contains
the final publisher text, and the packaged INSTALL describes alpha.3. This entry
does not claim publication or reuse candidate assets as release assets: the
VERSION commit must be rebuilt and verified, then public assets downloaded and
checked before the post-release documentation promotion.
