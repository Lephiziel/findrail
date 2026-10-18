# Alpha.5 source candidate verification

This record is for an unpublished source candidate, not a release announcement.
The public version remains `0.1.0-alpha.4`; the public alpha.4 tag, release and
eight assets are unchanged. Candidate archives are named alpha.5, built from a
clean exact source SHA, and report schema 8, API 1 and VSIX 0.1.0. Do not use
their schema-8 data directory with the published alpha.4 binary.

## Exact candidate

The reviewed source SHA and the CI run links are recorded with the pull request.
The PR's Alpha release candidate workflow must run every required gate on its
head SHA: source CI (including Go/vet/race/UI), all five native package jobs,
the extracted Linux browser journey, installed read-only VSIX, exact eight-file
aggregate, reusable asset verification, and the read-only release-state check
where applicable. A skipped or absent required job is not a pass. Main-push
release-state evidence is distinct from PR preview evidence; publisher must
remain skipped and no release may be created.

## Required candidate cases

- Production-packaged native Linux binary: exact version/revision/schema/target,
  safe extraction into fresh owned paths, archive resources, executable modes,
  CLI/search/MCP/diagnostics/portable snapshot, and desktop first/second owner,
  Quit/drain and packaged PDF child checks.
- Native CI, not cross-build, covers linux-amd64, linux-arm64, darwin-amd64,
  darwin-arm64 and windows-amd64. Native controlled adapters are not manual
  viewer, Gatekeeper or SmartScreen demonstrations.
- Legacy lifecycle: schema 2 and 6 paths remain; the actual published alpha.4
  binary creates the schema-7 synthetic index, committed indexing report and
  saved collection with notes/order. Candidate migration checks search,
  provenance, report, collection order/note and default legacy passage offset
  zero. A complete stopped backup is restored and read by actual alpha.4;
  alpha.4 rejects candidate schema 8 without changing it. An induced migration
  failure preserves the schema-7 data directory byte-for-byte. No downgrade is
  attempted.
- Extracted native Linux browser journey covers fresh start, folder selection,
  diagnostics download/schema, late Literal and DOCX passage selection,
  collection save/note/order/export/restart, PDF page 2, Advanced fallback,
  keyboard/focus, changed evidence/Forget invalidation and purge, and Quit.
  `serve --no-sync` and demo remain read-only with temporary collections.
- Generated VS Code and Claude Desktop MCP configs are exercised with their
  recorded executable/argument arrays by the SDK integration test, using the
  extracted Linux candidate executable in the native package job. MCP protocol
  smoke and installed read-only VSIX are separate checks; neither claims a GUI
  session in a third-party AI client or VS Code extension host beyond the test
  host that actually ran.
- The resource comparison uses the actual alpha.4 Linux amd64 release binary
  and extracted candidate on the same Linux host, with the same seeded 100-file
  synthetic workload and two measured queries after warmup. It characterizes
  resources only, not retrieval quality. Large profiles are separate runs.

## Integrated candidate gate

Both the standalone packaged journey and Alpha release candidate's required
`packaged-browser-journey` job invoke the same `scripts/packaged_journey.py`.
It runs the existing preset and evidence-bundle Chromium smokes alongside the
existing packaged passage, collection, diagnostics and lifecycle cases. All
consume the same extracted executable; none builds a second application.

With pinned Selenium 4.35.0 and matching local Chrome/ChromeDriver (the CI pins
are 139.0.7258.66), run:

```sh
python3 scripts/packaged_journey.py '/owned/install space 📦/findrail' \
  --candidate-sha <full-head-sha> --candidate-version 0.1.0-alpha.5 \
  --alpha3-binary /owned/alpha3/findrail --alpha4-binary /owned/alpha4/findrail \
  --evidence-fixture /owned/tools/evidence-collection-fixture \
  --report /owned/reports/packaged-journey.json
```

The workflows explicitly build the existing offline synthetic GitHub fixture
writer before the journey with `go build -o /owned/tools/evidence-collection-fixture ./scripts/evidence_collection_fixture.go`. Pass it via `--evidence-fixture`; the
runner never invokes `go run`, compiles an application, or selects one from PATH.
The helper only seeds a synthetic fixture; retrieval always uses the extracted
candidate. Its digest is recorded separately from the application digest.

`FINDRAIL_CHROME` and `FINDRAIL_CHROMEDRIVER` select local matched executables.
Before running cases the orchestrator checks exact SHA/version, native target,
clean BUILD-INFO, executable version and the source schema; the report includes
the executable SHA-256, tool/environment identity and actual case durations.
Failed identity or child cases return nonzero and still write a bounded report.
Explicit `--skip-browser` lists every browser case as skipped and reports
`incomplete`; it cannot satisfy full browser acceptance. A missing browser is a
failure. Child smoke groups are drained after success, failure and timeout so
servers/drivers cannot cross case boundaries.

The additional integrated transfer can also be run directly:

```sh
python3 scripts/portable_transfer_browser_smoke.py '/owned/install space 📦/findrail' \
  --candidate-sha <full-head-sha> --candidate-version 0.1.0-alpha.5 \
  --report /owned/reports/portable-transfer.json
```

Inputs are synthetic long Markdown/DOCX (Cyrillic, emoji and non-BMP characters),
a two-page PDF and the shared 260-result paging corpus. Installation, fixture,
ZIP and independent A/B/C index paths include spaces/Unicode. The test uses
real Sources UI/jobs for host-path ZIP export, inspection and explicit import;
ZIPs are outside the indexes and registered folders. It checks:

- Exact late quote code-point offsets, PDF page 2, saved note/order, Markdown
  and JSON export; a collection commit fences page continuation without mixing
  results or removing pinned evidence.
- Preset Save/Load/explicit scope/Apply, delayed real passage responses and an
  unsaved note; Apply issues no search, keeps evidence and needs explicit Search.
- A's export into B as a frozen source, no refresh/configure/native-opening
  controls, independent content after originals are removed, and idempotent
  import without renaming.
- Compatible B archive-source evidence mapped explicitly into C despite a
  same-name source. A deliberately lost real commit response exercises
  reconciliation, single submission and preservation of newer local edits.
- Path/media/hash/page/quote and live-to-frozen identity conflicts without
  partial collections; restart retains exact evidence and Forget cascades only
  related entries while preserving unrelated sources and separate exports.
- No browser storage/history writes or page requests outside the local server.

TASK 11 / PR #79 is a prerequisite for the snapshot UI part. It is merged into
main; missing controls fail a named case and are never treated as a successful
or skipped transfer. Acceptance is established only by the required gates on the
exact PR head. Existing TASK 11 and bundle smokes retain their malformed-file,
changed-after-inspect, cancellation, late-response and read-only checks; their
implementation is reused rather than copied here. For this candidate run, the
packaged journey report records the integrated transfer, preset and bundle cases
as passed with `browser_skipped=false`; consult the PR's linked Actions artifacts
for the exact identity and per-case results.

## Scope and limitations

Only product behavior exercised by the exact-SHA reports is claimed. Browser
automation uses a controlled local environment and synthetic files. Cross-build
is not native execution. The browser is not a manual test of every browser or
assistive technology. The SDK test is not a third-party client GUI test. Unsigned
macOS/Windows release dialogs and every external viewer remain manual platform
coverage unless an explicit controlled native test says otherwise. Candidate
results do not change alpha.4's shipped behavior or its historical verification
record.

## Publication after acceptance

Publication is a separate owner-authorized action. Prepare a release PR with the
deliberate `build/package/VERSION` change from `0.1.0-alpha.4` to
`0.1.0-alpha.5`, recording its exact head SHA and Actions run. Require every gate
on that head with publication skipped. Do not merge the VERSION change before
authorization: its merge enables the existing canonical-main gated publisher.
Every gate must pass again on the actual main commit before publication; a PR
run or rehearsal does not certify a later merge commit. Never create a tag or
release from a PR, fork, or rehearsal, and never replace historical assets.

After the release exists, download all eight public assets and resolve the tag
to its commit. Verify the downloaded bytes with:

```sh
python3 scripts/release_assets.py verify \
  --directory ./release-assets --version 0.1.0-alpha.5 \
  --revision <resolved-tag-commit-sha>
```

Record the main run URL, resolved tag SHA, manifest, filenames, sizes and
checksums. Confirm the five archives' BUILD-INFO matches that tag, schema 8 and
API 1, and the separate VSIX remains 0.1.0. A subsequent source/docs commit must
return `noop` for the unchanged published release. Only then update README
download links, current release notes, and the source-management,
portable-snapshots, evidence-collections and evidence-bundles guides to reflect
shipped UI behavior. Keep the 18 October source-cycle and alpha.3/alpha.4 records
historical.

Before an upgrade, stop every process using the index and copy the entire data
directory, including WAL/SHM files when present. Rollback restores that complete
stopped pre-upgrade backup to a separate location and uses the matching previous
binary; do not attempt an in-place downgrade or substitute a snapshot ZIP for a
database backup.
