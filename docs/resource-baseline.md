# Search resource baseline (candidate tooling)

This reproducible harness measures the shipped CLI, not a ranking score. It
creates fictional text, Go, DOCX, and one-page PDF files; no evaluation corpus,
external data, network access, or downloaded binary is used. It is intended for
resource characterization before a search optimization is proposed.

## Run

Build the desired binary first, then run (Python 3.9+):

```sh
go build -o /tmp/findrail ./cmd/findrail
python3 scripts/search_resource_check.py --binary /tmp/findrail \
  --documents 100 --seed 42 --queries 5 --report /tmp/findrail-resource.json
```

To measure two already-built binaries on the same host with an identical fixture
and query workload, add `--compare /path/to/second/findrail`:

```sh
python3 scripts/search_resource_check.py --binary /tmp/findrail-alpha4 \
  --compare /tmp/findrail-candidate --documents 1000 --seed 42 \
  --queries 5 --report /tmp/findrail-comparison.json
```

For a long-session source candidate, opt into HTTP cursor traversal and the
existing bounded evidence-bundle boundary checks with
`--source-cycle-boundaries`. This exercises page limits 20, 50 and 100 through
the actual `/api/v1/search/pages` endpoint and traverses no more than the API's
1,000-result window. It does not raise the legacy search limit or request an
evaluation result set above 100. Each size restarts traversal from the first
page; cursors are process-local. `total`, contiguous offsets and unique result
IDs are checked.

Example large run (deliberately not part of ordinary CI):

```sh
python3 scripts/search_resource_check.py --binary /tmp/findrail \
  --documents 100000 --allow-large --seed 42 --queries 5 \
  --source-cycle-boundaries --report /tmp/findrail-resource-100k.json
```

Large-profile reports are local artifacts and are not uploaded. Keep baseline
and candidate builds, their reports, and data directories isolated; never run an
older binary against a candidate-migrated index. A successful report does not
mean a refresh/cancellation-under-concurrent-search workload was measured; this
harness profile currently measures indexing, ordinary/paged HTTP search, and
evidence preview only.

The supported profiles are 100 (smoke), 1,000, 10,000, and 100,000. The latter
requires explicit `--allow-large`. Generation enforces a 256 MiB cap on both
generated text input and total corpus file bytes, plus a 5-minute/30-minute
overall deadline (small/large profiles); each run
uses separate temporary source and data directories and removes both on success
or failure, and on Ctrl+C or catchable POSIX SIGTERM. `--queries` is 1–100
measured repetitions per named synthetic query after an excluded warmup. Reports
contain only bounded metadata and aggregate results, never argv, paths,
environment values, or generated contents. A failed/partial run is marked
failed and must not be used as a baseline.

The report captures fixture version, seed and corpus SHA-256; binary SHA-256,
version, revision, Go runtime and schema; host OS/architecture; indexing wall
time and indexed counts; DB/WAL/SHM aggregate size after indexing and before
server startup; loopback server startup, first HTTP query and repeated persistent
HTTP latency p50/p95; and bounded search/evidence response bytes. The first
query is **not** a cold OS-cache measurement. Linux server RSS is sampled from
its own procfs status after each request when available; the sampled maximum is
not a process peak. Other platforms, inaccessible procfs, and indexing peak RSS
are reported as unavailable. Search and evidence sizes describe HTTP JSON.
Query IDs are fixed (`common`, `rare`); query text is not recorded. Result-shape
checks only establish that synthetic known terms returned matches; they are not
relevance or ranking metrics.

Run core engine allocation measurements separately:

```sh
go test ./internal/store/sqlite -run '^$' -bench '^BenchmarkSearch$' -benchmem
```

Setup/indexing is outside the timed loop. `ns/op`, `B/op`, and `allocs/op` apply
only to `Store.Search`, not CLI presentation or UI. The benchmark profiles are
100 and 1,000 documents. Use the same machine, binary build configuration,
fixture seed/version and query workload for comparisons; measurements on
different native hosts are not evidence of Go-code speedup.

## Measurements

### 2026-10-15 native review measurement

Host: Linux x86_64, AMD EPYC 9V74, Go 1.27.2, Python 3.12.14.
Binary built natively from reviewed diagnostics source SHA
`286101e64bc778733feec3c97e635ed8cfacef73` (`dev`, schema 7); binary SHA-256
`10d3badf162a3e7239871a284a145cf83a8dd6d1734eb18da4a362397af5b332`.
These are fixture-v2 harness measurements, not a published alpha.4 comparison
or the final alpha.5 integration result. Seed 42; one excluded request per query
followed by five measured requests each for `common` and `rare` (ten warm samples).

| Metric | 100 documents | 10,000 documents |
|---|---:|---:|
| Indexed / skipped | 100 / 0 | 10,000 / 0 |
| Synthetic PDF pages | 25 | 2,500 |
| Extracted indexed text bytes | 312658 | 4817314 |
| Index wall ms | 133.594 | 14215.546 |
| DB bytes immediately after index | 679936 | 15400960 |
| Server startup ms | 5.285 | 6.229 |
| First HTTP query ms | 2.897 | 20.936 |
| Warm HTTP p50 ms | 1.871 | 9.542 |
| Warm HTTP p95 ms | 2.327 | 25.2 |
| Search response bytes | 3075 | 3113 |
| Evidence response bytes | 746 | 749 |

WAL and SHM were absent at the recorded post-index point (zero bytes). Server
procfs RSS was unavailable on this host and is not inferred from parent memory.
Fixture identities (v2, including a >65,536-character document of each format):

- 100 documents: `f7be8b664cbbe8af0bde1954800685fd8dc9cbb47d0ab7069b84285544c8da91`.
- 10,000 documents: `a2e69b1a1a28c5617ae93f6295578f105730ca235b47e15604dc2a21c4ab1c08`.

Engine-only benchmark on the same host (`-benchtime=100ms`, setup excluded):
100 documents, 701,219 ns/op, 40,191 B/op, 685 allocs/op; 1,000 documents,
2,996,032 ns/op, 40,181 B/op, 686 allocs/op. Setup and teardown are excluded.
This short sample is not an end-to-end latency or speedup claim. Measure the
final integration candidate afresh.

## Limitations

The harness uses the production `index` CLI and loopback `serve` HTTP API; it does
not benchmark OS cold-cache behavior or process-peak RSS. The Go benchmark isolates
engine allocations but does not represent end-to-end
latency. PDF fixtures are synthetic, one page each. Filesystem, SQLite and
extractor behavior vary by host. Alpha package metadata may not expose a VCS
revision; reports use `unknown` where unavailable. No production search
semantics or schema are changed by this tooling.
