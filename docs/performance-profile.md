# Search profiling and benchmark results

## Scope and method

This is a reproducible Store-level baseline, not an HTTP latency or user-facing
p95 claim. The measured calls are `Store.Search` (legacy ranked search) and
`Store.SearchPage` (cursor/page API). Search request execution is timed; opening
SQLite and seeding the synthetic index happen before `ResetTimer`. Setup duration
is separately reported as `setup-us`. Seeding uses deterministic in-memory
synthetic text/DOCX/PDF-shaped `connector.Document` values and Store's normal
transactional upsert path; it intentionally does not include filesystem
enumeration or document extraction.

Default matrix: 100 and 1,000 documents; common, rare, and empty text queries;
title/path/source/format filters; page first/next/empty and PDF page attribution.
The mixed corpus has text, DOCX and PDF documents. `BenchmarkPDFSearch` uses 250
bounded PDFs with three page records per PDF, matches on pages 1, 2 and 3, and an
empty query result. No production endpoint, pprof listener, or runtime diagnostic
flag was added.

Large corpus matrix is explicitly opt-in with `FINDRAIL_BENCH_LARGE=1`; it adds
10,000 and 100,000 documents to `BenchmarkSearch` and `BenchmarkPageSearch`.
It should not be used with `-count 3` casually: each selected size builds its own
synthetic index. For example, a one-operation 100k common-query run is:

```bash
FINDRAIL_BENCH_LARGE=1 go test ./internal/store/sqlite -run '^$' \
  -bench '^BenchmarkSearch/documents_100000/common$' \
  -benchmem -benchtime=1x -count=1
```

Go benchmark CPU and allocation profiles use standard `-cpuprofile` and
`-memprofile` flags. The SQL-plan probe runs against a 201-document synthetic
Store index:

```bash
go test ./internal/store/sqlite -run '^TestSearchSyntheticQueryPlans$' -v
go test ./internal/store/sqlite -run '^$' \
  -bench '^BenchmarkPageSearch/documents_1000/first_common$' \
  -benchtime=3s -benchmem \
  -cpuprofile=/tmp/opencode/task22-artifacts/search-pages.cpu \
  -memprofile=/tmp/opencode/task22-artifacts/search-pages.mem -count=1
```

## Measurement identity

| Property | Value |
|---|---|
| Benchmark source commit | `c2a5bcb` (`Benchmark filters through the paged Store API`) |
| Profile binary SHA-256 | `df611b6f7f71fd89f932c25a0884dabb9b3e89df8dc760f572217d2afcac04fc` (`sqlite.test`) |
| CPU profile SHA-256 | `16fc4ffdd9dff0b8e0de55751e2a89eb8d317fca00a126198f33a13af5ccf40f` |
| Memory profile SHA-256 | `f6ef2da2fe7a37419dc2ac7988b6e7d74733a5dc9ae20a54354d2e18f94d532e` |
| Go | `go1.27.2-X:nodwarf5 linux/amd64` |
| Host | Linux/amd64, Intel Core i7-10700KF @ 3.80 GHz, `GOMAXPROCS=16` |
| Profile workload | 1,000 mixed synthetic documents; `SearchPage(commonmark, limit=20)`, first page; 3s benchmark duration |
| Profile run | 1.993 ms/op, 141,861 B/op, 1,154 allocs/op |

Raw profiles are intentionally outside the repository at
`/tmp/opencode/task22-artifacts/`; no database/index or raw profile is committed.

## Results

Three-run Store benchmark series; table shows the middle run (not a percentile).
Allocations are per operation. Results are host- and Go-version-specific.

| API / workload | Documents | Time | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `Search` common | 100 | 0.499 ms | 33,909 | 686 |
| `Search` rare | 100 | 0.214 ms | 5,814 | 140 |
| `Search` empty | 100 | 0.213 ms | 3,577 | 85 |
| `Search` common | 1,000 | 1.750 ms | 33,884 | 687 |
| `Search` rare | 1,000 | 0.400 ms | 20,621 | 436 |
| `Search` empty | 1,000 | 0.257 ms | 3,576 | 85 |
| `SearchPage` first common | 1,000 | 1.953 ms | 141,677 | 1,154 |
| `SearchPage` next common | 1,000 | 2.037 ms | 129,855 | 1,161 |
| `SearchPage` empty | 1,000 | 0.304 ms | 8,225 | 175 |
| `SearchPage` title filter | 1,000 | 2.144 ms | 141,863 | 1,158 |
| `SearchPage` path filter | 1,000 | 1.303 ms | 129,509 | 1,162 |
| `SearchPage` source filter | 1,000 | 1.916 ms | 141,848 | 1,158 |
| `SearchPage` text format | 1,000 | 2.062 ms | 128,940 | 1,153 |
| `SearchPage` DOCX format | 1,000 | 1.136 ms | 146,341 | 1,153 |
| `SearchPage` PDF page attribution | 1,000 | 3.879 ms | 162,849 | 1,754 |
| `Search` PDF common (250 PDFs) | 250 | 5.184 ms | 54,136 | 1,227 |
| `Search` PDF match on page 1 | 250 | 3.807 ms | 54,142 | 1,227 |
| `Search` PDF match on page 3 | 250 | 3.790 ms | 54,134 | 1,227 |
| `Search` PDF empty | 250 | 0.318 ms | 3,719 | 86 |
| `Search` common (opt-in) | 10,000 | 17.093 ms | 41,496 | 695 |
| `Search` common (opt-in) | 100,000 | 162.196 ms | 41,672 | 697 |

Setup was excluded from those `ns/op` values. Representative setup metrics were
18.810 ms for 100 documents, 138.579 ms for 1,000 mixed documents, 64.064 ms for
250 PDFs, 1.453 s for 10k, and 15.712 s for 100k. Index preparation is
therefore a material separate cost at larger sizes.

## Profile and query-plan observations

The CPU profile sampled 4,220 ms total CPU. SQLite's VDBE accounts for 9.72%
flat and 75.12% cumulative samples; B-tree movement is 13.98% cumulative and
the FTS5 BM25 function is 40.28% cumulative. This indicates
that query execution/ranking, not Go request setup, dominates this workload.
Profile sample totals are CPU samples, not wall-clock shares; they must not be
added as exclusive percentages.

The allocation profile aggregates the full 3-second run (270.17 MB allocated).
`fitSearchPage` accounts for 69.32% cumulative allocated space, including JSON
size-candidate serialization and cursor construction; `searchPageTx` accounts
for 24.82% cumulative. This locates allocation work but does not establish that
a replacement can preserve the exact 512 KiB response budget more cheaply.
Per-operation allocation figures in the table are the more direct steady-state
comparison. No production optimization was made: there is no measured before /
after production win to justify changing response sizing, query behavior, or
reader lifecycle. TASK 41 selected a separate ranked-query hot path; its
measured comparison is recorded below.

Synthetic `EXPLAIN QUERY PLAN` observations:

- Exact count: FTS virtual-table match, then document primary-key lookup.
- Ranked rows: FTS virtual-table match, document primary-key lookup, covering
  source index lookup, and a temporary B-tree for `ORDER BY`.
- PDF attribution: page FTS virtual-table match, page primary-key lookup, and a
  temporary B-tree for rank/page ordering.

These plans and timings confirm that count, rank/sort, and per-result PDF page
lookup have different costs. They do not show that exact count or attribution is
unnecessary or incorrect. The benchmark does not include HTTP serialization,
transport scheduling, concurrent request contention, filesystem extraction, or
production corpus distributions; its timings must not be extrapolated to HTTP
p95.

### 2026-10-31 TASK 41 ranked-score follow-up

This is a same-host Store benchmark comparison between the fetched `main`
source `4c0c94e1663804344b1ae1cbcdc3a346208e688a` and candidate
`b8377d8dfd941c99c64c77435a4ae2a91f22a52b`. Both used Go `go1.27.3`
(`linux/amd64`), CGO enabled, `GOMAXPROCS=1`, the same synthetic 1,000-document
fixture and `-benchtime=2s -count=5`. The host was Linux
`adc-sandbox`, Intel Xeon Platinum 8370C @ 2.80 GHz. Baseline/candidate
`sqlite.test` SHA-256 values were
`45e038a750d34f8f3bcea1e990703028d0b33e6cd0af7e332175b7c4498bd17b` /
`8113b294bd3b67088846a7e3460e18ccdc2b4f4b91f68b0db9646c58810967eb`.
The candidate CLI SHA-256 was
`cb46fd8fed98355649611b1c9d2f5599da6911b367eceac35f95722323a5e0f8`.

The production change uses the selected `-bm25(...)` value as a SQL alias and
orders by that alias descending, preserving the previous relevance direction
and document-ID tie-break in both `Search` and `SearchPage`. Median results
from the five samples were:

| Workload | Baseline | Candidate | Difference |
|---|---:|---:|---:|
| `Search` common | 2.363 ms | 2.339 ms | -1.0% |
| `Search` rare | 0.458 ms | 0.439 ms | -4.1% |
| `SearchPage` first common | 2.689 ms | 2.523 ms | -6.2% |
| `SearchPage` next common | 2.664 ms | 2.655 ms | -0.3% |
| `SearchPage` PDF attribution | 4.815 ms | 4.992 ms | +3.7% |

This is a bounded Store-level signal, not an HTTP p95 claim; PDF attribution
did not improve and no broad speedup is claimed. Allocations were unchanged
for the compared page workloads. Correctness checks covered exact totals,
stable IDs/order, legacy parity, filters, PDF page-local provenance, cursor
contracts and the diagnostic query-plan probe. Raw benchmark outputs remain
outside git; their SHA-256 values are recorded in the task handoff artifacts.

## Correctness and limits

No ranking weights, query grammar, schema, API, cursor limit, or response cap
changed. The production query only reuses the already selected BM25 score for
ordering. Existing Store and HTTP tests exercise stable tie order,
title-only PDF page 0, multi-page PDF attribution and provenance, advanced
positive/negative branches, cursor refresh fences, cancellation/Close cleanup,
the 1,000-result window, and result boundaries. The new synthetic performance
test additionally checks that `%` and `_` in path filters are literal and emits
the query plans above. Large fixture profiles are opt-in; profile artifacts are
local-only. The A/B series was limited to the ranked query path; it is not an
HTTP p95 or broad user-latency claim.
