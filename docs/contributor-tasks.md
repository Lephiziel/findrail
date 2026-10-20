# Contributor tasks

These are current, bounded entry points based on the open issues and repository
gaps checked on 20 October 2026. Check each linked issue for updates before
starting. A failed, partial, or skipped platform test is useful when reported
honestly; do not turn CI or a simulated run into a claim of manual validation.

## Go coding

### Add structure-preserving DOCX XML fuzzing — [issue #51](https://github.com/Lephiziel/findrail/issues/51)

- **Gap:** `FuzzExtract` mutates complete ZIP bytes, so many inputs fail in ZIP
  parsing before exercising the bounded WordprocessingML parser. A complementary
  target must preserve a valid synthetic DOCX envelope and vary the XML part.
- **Read:** `internal/extract/docx/docx_test.go` (notably `packageBytes` and
  `packageWithParts`), `internal/extract/docx/docx.go`, and `docs/docx.md`.
- **Change/result:** add `FuzzDocumentXML` beside the existing raw-package target;
  keep its input bounded to 64 KiB and seed ordinary paragraph, Unicode/tab/break,
  nested table, namespace-spoof and DTD/entity-rejection inputs. Preserve production
  limits and the existing raw ZIP fuzzer. Successful output must remain valid UTF-8
  and within `MaxText`; errors must not return partial text.
- **Check:** `go test ./internal/extract/docx`; then
  `go test ./internal/extract/docx -run '^$' -fuzz '^FuzzDocumentXML$' -fuzztime=30s -parallel=2`;
  and `go vet ./internal/extract/docx`. Report Go version and fuzz duration.
- **Size/experience:** small; comfortable with Go tests and basic fuzzing.
- **Parallel boundary:** the DOCX extractor/test files belong to this task. Keep
  parser limits and production behavior unchanged; use synthetic in-memory data
  only. This coding task is intentionally left for an external contributor.

## Human language review

### Independently review Turkish and/or English search labels — [issue #5](https://github.com/Lephiziel/findrail/issues/5)

- **Gap:** the synthetic corpus and initial labels were AI-assisted; an
  independent human-language judgment is still missing. Automated evaluation
  passing is not a substitute.
- **Read:** `examples/evaluation/documents/`, `examples/evaluation/queries.json`,
  and `docs/search-evaluation.md`.
- **Change/result:** a person fluent in the reviewed language checks the text,
  query intent, expected paths and rationale. A concise dated review may identify
  only disputed query IDs, their current expectation, proposed correction and
  reason; there is no need to rewrite the corpus. State which language and cases
  were personally reviewed and disclose any AI assistance. Leave the other
  language explicitly unreviewed if applicable.
- **Check:** reproduce the guide's evaluation with the published/current version
  identified, a fresh separate data directory and only
  `examples/evaluation/documents` as input. Separate observed program output from
  language judgments; report mismatches before changing expected labels.
- **Size/experience:** small; requires human fluency in whichever language is
  reviewed, not Go expertise.
- **Parallel boundary:** do not change ranking, query semantics, or
  `internal/evaluation/*`, `internal/cli/evaluate.go`,
  `internal/cli/evaluate_test.go`, `internal/cli/queries.json`,
  `examples/evaluation/*`, or `examples/notes` as part of this review. Coordinate
  any proposed corpus edit through the issue; AI-assisted relabeling is not
  independent review.

## Hardware and manual validation

### Apple Silicon Finder first run — [issue #4](https://github.com/Lephiziel/findrail/issues/4)

- **Gap:** existing alpha.2 report covers CLI download/checksum and basic use,
  not Finder quarantine prompts or installed desktop launch. A separate actual
  alpha.5 first-run observation is still needed; keep the historical report intact.
- **Read:** the [alpha.5 release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.5),
  `build/package/INSTALL.txt`, `docs/release-smoke-alpha5.md`, and
  `docs/platform-installation.md`.
- **Change/result:** on actual Apple Silicon/macOS 13+, report archive/version,
  checksum, browser, prompts and exercised/skipped installation, launch/relaunch,
  quit, optional original opening, and collection restoration steps. Do not
  bypass quarantine/security controls. Keep private paths, documents and index
  data out of the report.
- **Check:** follow the alpha.5 acceptance sheet using fictional files and a
  separate test data directory. Clearly distinguish Finder/manual observations
  from native CI and browser automation. A blocked or failed step is still useful.
- **Size/experience:** small; requires access to Apple Silicon hardware and
  macOS, no Go development.
- **Parallel boundary:** report-only platform validation; do not rewrite the
  earlier alpha.2 record or claim all Mac configurations are certified.

### Windows desktop first run — [issue #52](https://github.com/Lephiziel/findrail/issues/52)

- **Gap:** Windows CI checks packaging, but does not establish what a person sees
  after downloading an archive, using the per-user installer, Start-menu shortcut
  and external viewer on actual Windows.
- **Read:** the [alpha.5 release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.5),
  `build/package/INSTALL.txt`, `docs/release-smoke-alpha5.md`,
  `launcher/windows/`, and `docs/platform-installation.md`.
- **Change/result:** on actual Windows, record OS/CPU/browser, archive/version and
  checksum; test download/extraction, per-user install, repeated launch, tab close
  versus Quit, explicitly enabled original/folder opening, saved collection
  restoration and uninstall/data preservation. Report security prompts and
  failures precisely; never recommend a system-wide security bypass. Keep personal
  data and indexes out of the repository.
- **Check:** use fictional files and a separate test data directory. Identify
  each step as exercised, failed or skipped, and separate manual GUI/viewer results
  from CI/browser automation.
- **Size/experience:** small; requires actual Windows access, no Go development.
- **Parallel boundary:** report-only platform validation; do not treat native CI
  as a manual first-run report or claim all Windows versions/viewers are certified.

## Voluntary product feedback

### First search and return-use feedback — [issue #3](https://github.com/Lephiziel/findrail/issues/3)

- **Gap:** the alpha.5 release record describes owner acceptance and automated
  checks; independent voluntary retrieval and return-use outcomes remain pending.
- **Read:** the [alpha.5 release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.5),
  `docs/demo.md`, `docs/local-alpha.md`, and the issue's current steps.
- **Change/result:** optionally try installation, a remembered-word search and
  preview, then a saved collection and later return. A concise report may say
  where you stopped, whether the expected item was found/restored, and what helped
  or got in the way. Use fictional files for installation tests. Do not post
  private documents, indexes, credentials or sensitive real queries.
- **Check:** no code test is expected. Identify the actual version/platform and
  whether the observation was manual; distinguish first-use feedback from a later
  return. Participation is voluntary.
- **Size/experience:** a few minutes initially; optional return after some days;
  no development experience required.
- **Parallel boundary:** user feedback only. Alpha.2/alpha.4 reports remain
  historical and are not evidence for alpha.5.

## Completed in current code

### English original-file action labels — [issue #48](https://github.com/Lephiziel/findrail/issues/48)

The current main source uses **Open original** and **Open containing folder** in
both result actions and Preview. This was delivered by [PR #88](https://github.com/Lephiziel/findrail/pull/88),
merged as `303714cb0a813f95a52ad073582aedb949b2b429`; the labels live in
`internal/transport/http/web/index.html` and
`internal/transport/http/web/preview_ui.mjs`. PR #88 records the capability/identity
checks and reports `node --test internal/transport/http/web/*_test.mjs` among its
validation. Recommend closing #48 as completed; do not reimplement the labels.

## Current release boundaries

The published release is **0.1.0-alpha.5**, with schema 8, Client API 1 and VSIX
0.1.0. The source package version remains `0.1.0-alpha.5`. See
`docs/release-alpha5-verification.md` for the recorded evidence and limitations.
Automated GUI journeys are not manual Finder/Windows experience, and owner smoke
tests are not independent user reviews. Do not infer beta support, OCR, semantic
search, private/cloud/team/auth features, automatic updates or telemetry from
these contributor tasks.
