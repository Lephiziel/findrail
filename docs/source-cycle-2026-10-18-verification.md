# Integrated source-cycle candidate verification — 2026-10-18

This is the verification protocol for the unpublished schema-8 source cycle.
It does not describe the published alpha.4 downloads or announce alpha.5.
`build/package/VERSION` stays `0.1.0-alpha.4`; preview archives use
`0.1.0-alpha.5`, schema 8, Client API 1 and extension version 0.1.0.

## Source and dependencies

Integration started from clean main
`c11622c277b4a72a2618e734470feec54febde17`. The original source baseline
`6b157fe3a6fe2f3f3808732ce72a8e74a118f763` is its ancestor. All required
dependencies are merged; the pagination task was delivered in three stages.
The candidate gate also includes merged PR #80 (`71b3da4e50f8a46de6da68d281b13299c8c0565e`),
which adds the independent A/B/C portable transfer and runner cleanup checks.

| Task | Merged PR | Merge revision |
|---|---|---|
| UI lifecycle modules | #64 | `9754957166f81719d187f94ec3c312dd7f97a8a3` |
| MCP passage windows | #63 | `602b4f29707f606b2d4330fdbecaa5ff3df09123` |
| Bounded page query | #65 | `f185a06bc9ce7dca2d40db0797d7a7509bc3c1e8` |
| Cursor lifecycle/fence | #66 | `ff23b1ac7e6f983a156668c32d31621aa51bcedd` |
| HTTP pagination | #67 | `7251a7992b0a1e515904bd2ddead174add7eccbc` |
| Browser pagination | #69 | `615ba27541c479ce9b40a62e9bae8658803e0fa1` |
| Evidence bundle API | #68 | `df3b3aeeb64acedd17a2c9cc824a3c5c25e8d6a9` |
| Evidence bundle UI | #71 | `8b89385cff3cc71c7d221e46523b2fa11268aa87` |
| Strict bundle codec | #61 | `584b52306359b3f04b6dcba1aedde1dfe18b885b` |
| Doctor integrity | #62 | `d3f5990376b8f217e2f84c3f583128041a97305e` |
| Preset files | #78 | `1c947a5c1797fbf18db5a1ad1ec66042e2ed4caa` |
| Portable snapshot jobs | #70 | `75229ce7827b3bb74c8cbc2572b9341912eec3ae` |
| Portable snapshot UI | #79 | `c11622c277b4a72a2618e734470feec54febde17` |

## Obtaining the exact-HEAD record

Open [PR #81](https://github.com/Lephiziel/findrail/pull/81), choose the
**Alpha release candidate** check for its current head revision, and open that
workflow run. PR preview automatically sets publication to false. The production
packager checks out the head SHA directly, requires a clean tree, and safely
extracts each archive into an owned path containing spaces and Unicode.

Download these artifacts from that same run:

- `release-verification-<HEAD>`: `verification-summary.json`, including exact
  SHA, run URL/attempt, OS/toolchain, five native records, browser per-case
  progress, all eight asset sizes/digests, and resource reports.
- `release-package-<HEAD>-<target>`: the target archive, its `verification.json`
  and any target resource reports. Native records remain in separate artifact
  directories during aggregation so identical report filenames cannot overwrite
  another platform's evidence.
- `release-browser-journey-<HEAD>`: `packaged-journey-report.json`. Progress is
  atomically saved before and after each case and uploaded on failure too. An interrupted,
  failed, skipped, or absent case is not accepted as a pass.
- `release-assets-<HEAD>`: exactly five native archives, the installed-client
  VSIX, `release-manifest.json` and `SHA256SUMS.txt`. Verification reports and
  resource JSON are outside this eight-file set.

`candidate_record.py summary` rejects missing/duplicate target records, another
revision/version, incomplete browser coverage, different native/archive hashes,
and a browser binary digest that differs from the packaged Linux binary. It
requires every named acceptance case with a finite nonnegative duration, both
complete child journeys (the common browser and A/B/C transfer), their matching
binary digests, all four Linux resource records, the two pinned comparison
revisions, and the actual paging/JSON/snapshot boundary measurements. A single
synthetic passing case or an omitted resource report cannot certify the gate.
The reusable final verifier independently rechecks the complete asset set.
The publisher, its triggers/permissions and publication condition are unchanged.
Acceptance also requires `release-state` to return `noop` for immutable alpha.4
and the `publish` job to be skipped.

The final SHA and archive digests belong in these generated records and the PR
report, not in a self-referential commit containing its own hash. A documentation
tail commit needs a new run on its head like any other commit.

## Case protocol

| Case | Exercised behavior and evidence |
|---|---|
| Native packages, five targets | Production packaging, exact BUILD-INFO/schema/cleanliness, executable version, search/MCP/diagnostics/snapshot smoke, first/reused desktop owner, packaged PDF child and drain. Executables run on matching Linux amd64/arm64, macOS amd64/arm64 and Windows amd64 runners. |
| Common browser, empty index | Real pinned Chrome **139.0.7258.66** and matching driver; UI Add folder with Unicode path, text/DOCX/PDF ingestion and schema-8 diagnostics download. |
| Common browser, pagination | 260 synthetic matching documents; Next beyond 100/237, Previous/First, changed page size, backend ID/order agreement, a delayed real continuation discarded after a new search, stale-cursor recovery after a real corpus commit, and retained evidence/notes. Inventory delivery is held one response behind only while testing the cursor fence; page requests still use the production API. |
| Common browser, passage windows | Late MD/DOCX literal matches, manual Previous/Next text, exact selected quote, PDF page 2 and Advanced title-only fallback. |
| Common browser, bundle transfer | Notes/reordering/save, dirty-export refusal, JSON download, second data directory with a different source ID, explicit mapping, preview/confirmation, atomic import, exact text/note/order/offset comparison and restart restoration. |
| Common browser, preset | Save/load file, preview, explicit remap, Apply with no implicit search/storage/history write, then explicit Search. Existing selected evidence is retained. |
| Common browser, source transfer | New host output path, export job and verified metadata, inspect/claim/import job, frozen DOCX search, `already_imported`, restart, Forget/re-add and a delayed pre-Forget preview that must not reopen. |
| Independent A/B/C transfer | Export a live source from A, import frozen sources in B and C, verify exact non-BMP offsets and PDF page 2, delete originals in A, transfer a compatible frozen-source bundle B→C, retain newer local edits after an uncertain commit, reject incompatible live/bundle identities, invalidate claims/cursors after restart, preserve unrelated evidence on Forget, and check read-only serve/demo. |
| Focused browser cases | Existing passage/keyboard/Quit, snapshot error/cancelled-response, persistent collection, temporary collection, bundle and preset scenarios run separately on the same extracted candidate. This keeps fault handling out of one oversized browser case. |
| Generated MCP configurations | Actual extracted candidate via `FINDRAIL_MCP_TEST_BINARY`, VS Code/Claude Desktop profiles × protocols 2026-07-28/2025-11-25, late/manual passage navigation, UTF-8/EOF and source isolation. |
| Installed VSIX | Actual installed 0.1.0 extension runs against extracted Linux candidate with Client API 1 and legacy Search compatibility. |
| Doctor | Lightweight ordinary report; opt-in completed structural/FK checks, real FK conflict, malformed schema/incomplete result, exit codes and unchanged database bytes. Full stopped-directory fingerprints are recorded before/after. SQLite `mode=ro` may create empty WAL and bookkeeping SHM files; byte-for-byte directory identity is explicitly not claimed. |
| Upgrade/restore | Pinned published alpha.3/schema6 and alpha.4/schema7 executables, stopped full-directory backup, schema8 migration, reports/provenance/collection notes/order/offsets, restore with the old executable, old-binary rejection of schema8 and induced migration008 rollback. Schema2 remains a synthetic fixture. |
| Protected evaluation | Existing labels/fixtures/parser/ranking/legacy 1–100 limit are unchanged; the compiled corpus, complete negative report, read-only checks and >100 incomplete-result rejection run against the candidate. This is not independent label review. |
| Negative atomicity | Relevant Go/race/HTTP/store tests cover bundle digest/source/claim conflicts and quota, malformed/changed/oversized snapshot input and invalid output paths, cancellation/Forget/restart and cursor expiry. Browser tests check visible outcomes, focus and late responses. |

## Same-host resources

The existing seeded `search_resource_check.py` harness measures candidate versus
published alpha.4/schema7 and versus the production-packaged source baseline
`6b157fe3a6fe2f3f3808732ce72a8e74a118f763`/schema8 on the same Linux runner.
Both comparisons use fixture v2, 100 documents, seed 42, two measured requests
per query after warmup. Reports include corpus and binary hashes. These small
samples characterize resources; they do not establish relevance or scale.

An opt-in profile reuses the same harness and 1,000-document fixture to measure
the 500-result cursor window and JSON preview at the codec's 2 MiB quote limit,
then rejects one byte beyond it without creating a collection. The collection's
retained-text quota is stricter: each quote is retained in evidence and excerpt,
so 1 MiB of quotes fits its 2 MiB text budget, while a codec-valid 2 MiB bundle
returns `quota_conflict`. Both boundaries are measured without changing quotas. The
existing `portable_resource_check.py` measures the 8 MiB per-document snapshot
boundary. Responses/request sizes, elapsed time and available RSS observations
are kept in machine records. No 100k run or all-OS performance claim is made.
Compare timings within the same report/host, and investigate substantial changes
without moving the baseline. Sampled RSS is not a process-peak guarantee.

## Coverage boundaries

Passing source checks alone are insufficient: every required production/native,
browser, SDK, installed VSIX, aggregate and reusable verification gate must pass
on the final PR head. Native runs do not replace manual Gatekeeper/SmartScreen,
external viewer or assistive-technology checks. Browser and SDK fixtures are
synthetic; SDK runs are not third-party client GUI tests. The local execution
container can prohibit Chrome's Unix socket setup; real browser results then
come from the pinned Linux CI runner, not Node assertions or a cross-build.
Historical alpha.3/alpha.4 assets and verification/media records remain intact.
