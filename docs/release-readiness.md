# Release readiness

This record separates shipped software, source-candidate evidence, and product
release decisions. A green synthetic check is not user evidence. The validator
(`python3 scripts/readiness.py`) cannot set `final_release=true`; publication and
the v1 decision remain with the project owner.

## Fixed v1 scope

Selected local folders → lexical passage search → source provenance and
freshness → quotes and saved collections → safe upgrade and rollback.
OCR, semantic search, private GitHub/cloud/team/authentication, new connectors,
and automatic updates are outside v1 scope. Public GitHub snapshots, MCP and the
VSIX are existing optional surfaces; regressions matter, but they do not create
a new local-v1 SDK-contributor gate.

## Candidate and evidence status

| Gate | Required evidence | Status at this record |
|---|---|---|
| Source/strict candidate summary | Exact candidate SHA, existing strict root (19), transfer (20), common browser (7), four resource records | Pending this integration head; preserve the strict TASK12 summary |
| Required exact-head CI | `go test ./...`, `go test -race ./...`, `go vet ./...`, Python/package/release tests, browser tests | Pending final head CI |
| Native packages | Five target jobs and archive/binary provenance | Existing workflow contract; final exact-candidate run pending |
| Packaged browser, MCP configs, installed VSIX, upgrade/restore, integrity | Existing packaged workflow and extracted candidate evidence | Exact-candidate result pending |
| Freshness/browser, crash/recovery, installer-native | Candidate-bound records with complete case lists and durations | New supplemental records required; pending |
| Scale 10k / 100k | Real TASK17 measurements after product changes, exact candidate and fixture | Pending; small source benchmark is not a substitute |
| Manual Windows first run | Human observation on relevant candidate | No report recorded |
| Manual Apple Silicon first run | Human observation on relevant candidate | No report recorded |
| Voluntary ordinary use/return | User-submitted report(s), not CI | No reports recorded |
| Language review / owner decision | Human issue review and decision | Pending; not assigned to this validator |

No measurements are asserted by this record without retained, candidate-bound
reports. The existing scale test limits remain unchanged; absent 10k/100k reports
are incomplete, not pass. The source candidate is not thereby declared a public
beta or v1. There are no known blockers claimed from evidence that has not yet
been run; exact-candidate failures must be triaged, not hidden by reruns.

## Decision rules

The strict candidate summary remains mandatory; supplemental records cannot
replace its prior 19/20/7 cases or four resource records. Each new record must be
unique, bounded, content-free, and tied to the exact candidate SHA/version and,
where applicable, binary/archive/fixture hashes. Missing, duplicate, failed,
skipped, interrupted, inconsistent or unmeasured cases fail closed. A source
comparison baseline may use a different binary only when explicitly identified
as a `reference`. Indexes, profiles, raw logs and document/query text are never
retained here.

The validator reports software status separately from release readiness:
`pending`, `blocked`, or `eligible-for-owner-decision`. Eligibility is not a
release authorization. Voluntary reports are never uploaded automatically and
their absence does not hold a completed code PR in draft.

## Platform and date constraints

The five native jobs are automated package evidence, not manual Finder,
SmartScreen, accessibility, or first-run observations. Full public beta requires
the Windows and Apple Silicon manual results above, or an explicit owner decision
to limit support. Mid-December 2026 is a planning ориентир only; indexing or
migration issues can move it. No release date is promised.

## Next step

Run the packaged acceptance on the final merged TASK20 head, retain only bounded
candidate-provenance records, perform fresh 10k/100k runs using the existing
limits, then collect the two manual platform reports. The owner reviews voluntary
usage quality and remaining issues before any beta/v1 decision. This PR does not
change VERSION, tags, release assets, or publish a release.
