# Performance CI

## Quick CI

The existing `ui` job runs source-only contract tests for the performance series,
mixed-load soak, native acceptance kits, manual evidence intake, and alpha.5 media
tools. These tests do not run the long measurements, download release assets, or
need a GUI. Existing Go, race, cross-platform, packaging, and browser gates remain
unchanged.

## Explicit measurement workflow

`.github/workflows/performance-check.yml` is a read-only `workflow_dispatch`
workflow on canonical `Lephiziel/findrail` `main`. Provide a full candidate commit
SHA, optionally a full baseline commit SHA (default alpha.5 source
`cb53ee9638f69b47b386921e41368f3b66ceddde`), suite (`series`, `soak`, or `all`),
and declared duration (2700 seconds for series, 1800 for soak, 4500 total for
`all`). Each job validates the dispatch ref, repository, SHA syntax, and exact
checkout identity before doing work. Never dispatch from an untrusted ref.

Each selected job packages its source on its own Linux runner using the production
`scripts/package.py` path, `build/package/VERSION`, Go 1.27.x and the packager's
default compiler flags. The series job packages candidate and baseline with the
same toolchain and flags on that host. It records source revisions, Go/compiler
environment, OS/CPU, version output, and archive and executable SHA-256 values
before measurement. The independent soak job records its own host and binary
identity; its machine/resource samples must not be treated as identical to the
series host. The baseline default is source, not the published alpha.5 binary.

The series uses TASK21's runner: 10,000 and 100,000 documents, seed 42, three
repeats, 100 queries per family, AB/BA order, and a 2700-second measurement
deadline. The separate soak job uses TASK23's long profile for 1800 seconds and
requires at least 12 fully completed mixed-load cycles. It waits for successful
startup scans of each known fixture source before paging after every server
start/restart. A stable paging window may restart from page one up to three times,
only when the API explicitly returns `cursor_stale`; discarded pages are never
mixed with the accepted generation, and each restart is counted. Other 409 codes,
malformed responses, and exhausted retries fail the run.

Long soak continues real mixed-load cycles through the target window rather than
padding fast cycles with idle time. Its hard deadline is the target plus at most
60 seconds, capped at 3600 seconds; the final 15 seconds are reserved for cleanup,
leaving bounded time to finish an in-flight cycle after the target. Short soak
still ends after three cycles. Job limits are 60 minutes each. Neither suite reduces
its sample count or workload to fit a runner. A timeout or capacity limit is an
incomplete/failed result, not a pass. Failure reports retain partial latency
summaries and completed-operation counters, plus bounded cycle/generation/case,
HTTP status, and allowlisted API-code diagnostics; they exclude request and
document content.

Both measurement jobs set `PYTHONDONTWRITEBYTECODE=1` before Python starts and
recheck exact revisions and clean source status after packaging, before measuring.

### Reading results

Actions artifacts are retained for 14 days. They contain bounded JSON reports and
provenance only; never upload databases, WAL files, fixture contents, user reports,
credentials, or unbounded logs. Artifact upload uses `if: always()` so partial
reports and provenance remain available on measurement failure. Upload failure is
visible as a failed step. Review each job's report status and completeness, then
compare per-family/per-size p95 summaries and paired ratios. Reports explicitly
qualify source comparison and record variation; a shared runner's absolute
`p95 < 150ms` is not a blocking gate. Correctness/tool errors remain failures.

There is no relative regression threshold or performance pass badge. Shared-runner
noise, incomplete samples, and slow-but-valid data must be reported as-is. A single
successful run is not an SLO. Link the workflow run and artifacts in the handoff,
including exact source SHAs, binary/archive hashes, observed values, runner limits,
and status. Do not infer a product claim or publication readiness from these runs.

## Dispatch recipe

After merge, go to **Actions → Explicit performance check → Run workflow** on
`main`; enter the exact full `main` SHA as candidate, leave baseline at its
alpha.5 default, choose `all`, and set declared duration to `4500`. This starts two
independent jobs, each with its own 60-minute timeout. For one suite, use `series`
with `2700` or `soak` with `1800`. Record the resulting workflow URL and artifact
links. A workflow dispatch from a PR branch is intentionally rejected.
