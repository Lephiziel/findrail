# Search resource baseline (candidate tooling)

This reproducible harness measures the supplied production CLI, not a ranking
score. It creates fictional text, Go, DOCX, and one-page PDF files; it does not
use an evaluation corpus or access the network. The caller supplies an explicitly
prepared binary; the harness never downloads or rebuilds one. It characterizes
resources before a search optimization is proposed.

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
page; cursors are process-local. `total`, contiguous offsets, unique IDs and
identical ordering across page sizes are checked. Page request latencies include
all traversal requests (no page warmup is excluded); sample count is the number
of pages (10/20/50 for limits 100/50/20 at the full window).
The legacy `paging_window` top-level `pages`, `returned`, `beyond_237`,
`max_response_bytes`, and `latency_ms` fields remain populated from limit 100 for
compatibility; per-size details are additive under `page_sizes`.

Example large run (deliberately not part of ordinary CI):

```sh
python3 scripts/search_resource_check.py --binary /tmp/findrail \
  --documents 100000 --allow-large --seed 42 --queries 5 \
  --source-cycle-boundaries --report /tmp/findrail-resource-100k.json
```

For a matched 10k source-baseline comparison, build the baseline from commit
`bb5b4d80a7558b7a9dd3a7749f519eec5cefe0ca` in its own checkout, then run:

```sh
python3 scripts/search_resource_check.py --binary /tmp/findrail-baseline \
  --compare /tmp/findrail-candidate --documents 10000 --seed 42 --queries 5 \
  --source-cycle-boundaries --report /tmp/findrail-resource-10k-compare.json
```

The baseline and candidate are indexed in separate temporary data directories.
The 100k opt-in comparison uses the same command with
`--documents 100000 --allow-large` and a distinct report path.

Large-profile reports are local artifacts and are not uploaded. Keep baseline
and candidate builds, their reports, and data directories isolated; never run an
older binary against a candidate-migrated index. On 10k/100k, this profile
changes a synthetic document and records one successful full-source refresh,
then changes a second document and cancels another full scan with SIGTERM while
a separate thread continuously issues HTTP search requests. It checks
successful reads, unsuccessful cancellation, and preservation of the previous
committed content. The existing 1k CI boundary profile retains paging and
evidence-boundary checks but records `refresh_cycle.status=not_measured`; it is
not a long-session measurement. This local process cancellation is not a UI
cancellation or cross-platform signal guarantee.

The supported profiles are 100 (smoke), 1,000, 10,000, and 100,000. The latter
requires explicit `--allow-large`. Generation enforces a 256 MiB cap on both
generated text input and total corpus file bytes, plus a 5-minute/30-minute
overall deadline (small/large profiles); each run
uses separate temporary source and data directories and removes both on success
or failure, and on Ctrl+C or catchable POSIX SIGTERM. `--queries` is 1–100
measured repetitions per named synthetic query after an excluded warmup. Reports
contain only bounded metadata and aggregate results, never argv, paths,
environment-variable values, or generated contents. A failed/partial run is marked
failed and must not be used as a baseline.

The report captures fixture version, seed and corpus SHA-256; UTF-8 generated
text bytes and on-disk synthetic source-file bytes; binary SHA-256,
version, revision, Go runtime and schema; host OS/architecture/processor; indexing wall
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

### 2026-10-19 extracted alpha.5 pre-release candidate

Both runs used the clean production Linux amd64 archive from
[candidate CI](https://github.com/Lephiziel/findrail/actions/runs/39249908887),
source `109678f69e76c2a0b64a64ef8566c41e570e1a13`, version `0.1.0-alpha.5`,
schema 8. Archive SHA-256:
`b61c48f4d69785722cba8f061debc4b88c37750833d7775052c2798b174e3350`;
CLI SHA-256: `ae62d49aa90562bdda74456b9b3604b7ff87b56679387d1d881f3573fa4a8fa7`.
No application was rebuilt. Host: Linux x86_64, Python 3.12.14; a specific CPU
model and server RSS were unavailable. Fixture v2, seed 42, five measured
requests per common/rare query after one excluded warmup per query (ten warm
requests total). Both runs completed bounded paging, full refresh, concurrent
reads and cancellation preserving the previously committed content.

| Metric | 10,000 documents | 100,000 documents |
|---|---:|---:|
| Indexed / skipped | 10,000 / 0 | 100,000 / 0 |
| Index wall seconds | 11.211 | 117.566 |
| Successful full refresh seconds | 9.254 | 97.970 |
| Warm HTTP p50 / p95 ms | 9.119 / 18.251 | 77.374 / 201.291 |
| Page request p95 ms, limits 20 / 50 / 100 | 47.186 / 41.561 / 49.397 | 201.458 / 258.699 / 238.633 |
| DB + WAL + SHM bytes at the recorded post-index point | 15,417,344 | 153,427,968 |

The observed 100k warm p95 exceeds the 150 ms engineering target. With ten
samples, no same-host reference, and no controlled cold cache, this is a
profiling lead rather than an SLO or a demonstrated regression. Page-fetch and
total-count costs should be characterized before selecting an optimization.
Raw bounded reports: [10k](resource-reports/2026-10-19-alpha5-109678f-10000.json)
and [100k](resource-reports/2026-10-19-alpha5-109678f-100000.json).
These records certify their named pre-release candidate; they do not certify
later release-preparation commits or a future public tag. The scale evidence
classifier's passed status means complete, consistent measurements, not that
the 150 ms latency target was achieved.

### 2026-10-18 native TASK17 source-candidate scale run

Host: Linux x86_64, Intel Core i7-10700KF, Go 1.27.2, Python 3.14.8. Fixture v2, seed 42, synthetic
corpus SHA-256 `a2e69b1a1a28c5617ae93f6295578f105730ca235b47e15604dc2a21c4ab1c08`
(10k) and `5680cc710acd926ad0114c69169eb4d31de428c657b27a5850ce2ef4aae6714a`
(100k). Candidate source revision `e7c7af2e004a59d0625049de1490e4969008f217`,
schema 8, binary SHA-256
`5807ade7a1a5e495661a10807a34ed7e52466245a06758c2a9c24d184d45ad10`.
Five measured requests per query (`common`, `rare`) after one excluded warmup
each (ten warm samples); page profile timing includes complete traversal to the
1,000-result window, separately for each limit.

| Metric | 10,000 documents | 100,000 documents |
|---|---:|---:|
| Indexed / skipped; synthetic PDF pages | 10,000 / 0; 2,500 | 100,000 / 0; 25,000 |
| Generated text / source-file / extracted bytes | 4,834,814 / 7,098,867 / 4,817,314 | 48,311,393 / 71,046,137 / 48,136,393 |
| Index wall ms | 7,907.697 | 80,511.550 |
| DB + WAL + SHM bytes after index, before doctor/search | 15,425,536 | 153,350,144 |
| Server startup ms | 7.104 | 7.310 |
| First HTTP request after startup ms (not cold cache) | 16.201 | 134.088 |
| Warm HTTP p50 / p95 ms (10 samples) | 8.232 / 15.964 | 65.468 / 127.146 |
| Page 20/50/100 full-window request p50/p95 ms | 31.343/40.536; 31.676/39.967; 33.029/42.886 | 153.075/168.763; 151.884/168.962; 157.140/175.426 |
| Page-window returned / total common matches | 1,000 / 5,000 | 1,000 / 50,000 |
| Sampled max server RSS (not process peak) | 28,868,608 | 26,849,280 |
| Successful refresh wall ms (single run) | 6,393.166 | 61,520.620 |
| Canceled refresh / concurrent HTTP reads | 262.112 ms / 17 successes | 262.405 ms / 2 successes |

The 10k run compares the production-source baseline at
`bb5b4d80a7558b7a9dd3a7749f519eec5cefe0ca` (binary SHA-256
`bb719e0bab52c12cb33dd5b17930c0f8fe6438999be76677fb06337f8cfeedb4`) and this
candidate on the same Linux host with matching fixture hash and identical
workload. Baseline versus candidate: index 7,888.520 / 7,907.697 ms; warm HTTP
p50/p95 8.478/16.344 / 8.232/15.964 ms (ten samples each); successful full
refresh 6,330.365 / 6,393.166 ms. During canceled refresh, the baseline/candidate
completed 17 / 17 concurrent search requests, with p50/p95 16.005/16.932 /
15.801/17.042 ms, and both retained the previously committed document. At 100k,
baseline/candidate index times were 80,760.358 / 80,511.550 ms; warm HTTP
p50/p95 65.684/127.120 / 65.468/127.146 ms; and successful refresh times
65,502.029 / 61,520.620 ms. Both canceled scans retained committed content and
completed two concurrent reads; their observed read p50/p95 were 138.804/139.756
and 149.897/151.627 ms. Page-request p95 across limits was 168–175 ms for both
binaries. These small samples are not stable regression estimates. This does not
establish the <150 ms goal as an SLO; an earlier draft run's aggregate warm HTTP
p95 was 169.536 ms, illustrating sensitivity to small samples and host
conditions. A narrow follow-up, if selected after review, is to profile
total-count and page-fetch query costs separately on this fixed 100k fixture
before deciding on targeted search optimization. The reviewed aggregate JSON
snapshots are [10k](resource-reports/2026-10-18-task17-10000.json) and
[100k](resource-reports/2026-10-18-task17-100000.json). Future harness runs write
reports only to the requested local path; nothing is uploaded automatically.
The measured
candidate SHA is the fetched base, not this tooling commit; these figures do not
include the harness change. RSS was sampled per request, not captured as peak;
native Linux synthetic results do not generalize to other OSes or real-user
workloads. The p95 <150 ms target remains an engineering goal, not an SLO.

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
