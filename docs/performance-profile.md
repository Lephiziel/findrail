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
| Benchmark source commit | `49dfd2e` (`Add reproducible search profiling benchmarks`) |
| Profile binary SHA-256 | `2de79037dc28723426660d83814be78829da473d5bd67912e1f022a2d08ceb2c` (`sqlite.test`) |
| CPU profile SHA-256 | `41d215df6cb5cae0ff192018d17831225a571c664025a1af36e3d0ecf019c2ed` |
| Memory profile SHA-256 | `f0b17159b49d4ab88d62055f8707c64c747f91b18ad08ad1e7cfddc8532d11b6` |
| Go | `go1.27.2-X:nodwarf5 linux/amd64` |
| Host | Linux/amd64, Intel Core i7-10700KF @ 3.80 GHz, `GOMAXPROCS=16` |
| Profile workload | 1,000 mixed synthetic documents; `SearchPage(commonmark, limit=20)`, first page; 3s benchmark duration |
| Profile run | 2.042 ms/op, 141,837 B/op, 1,154 allocs/op |

Raw profiles are intentionally outside the repository at
`/tmp/opencode/task22-artifacts/`; no database/index or raw profile is committed.

## Results

Three-run Store benchmark series; table shows the middle run (not a percentile).
Allocations are per operation. Results are host- and Go-version-specific.

| API / workload | Documents | Time | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `Search` common | 100 | 0.507 ms | 33,894 | 686 |
| `Search` rare | 100 | 0.218 ms | 5,818 | 140 |
| `Search` empty | 100 | 0.213 ms | 3,576 | 85 |
| `Search` common | 1,000 | 1.803 ms | 33,873 | 687 |
| `Search` rare | 1,000 | 0.405 ms | 20,620 | 436 |
| `Search` empty | 1,000 | 0.263 ms | 3,576 | 85 |
| `Search` title filter | 1,000 | 2.068 ms | 34,154 | 692 |
| `Search` path filter | 1,000 | 1.165 ms | 34,631 | 696 |
| `Search` source filter | 1,000 | 1.836 ms | 34,140 | 692 |
| `Search` text format | 1,000 | 1.931 ms | 34,051 | 688 |
| `SearchPage` first common | 1,000 | 1.999 ms | 141,645 | 1,153 |
| `SearchPage` next common | 1,000 | 2.086 ms | 129,842 | 1,161 |
| `SearchPage` empty | 1,000 | 0.310 ms | 8,224 | 175 |
| `SearchPage` PDF page attribution | 1,000 | 4.016 ms | 162,899 | 1,754 |
| `Search` PDF common (250 PDFs) | 250 | 5.252 ms | 54,135 | 1,227 |
| `Search` PDF match on page 1 | 250 | 3.787 ms | 54,138 | 1,227 |
| `Search` PDF match on page 3 | 250 | 3.786 ms | 54,139 | 1,227 |
| `Search` PDF empty | 250 | 0.318 ms | 3,720 | 86 |
| `Search` common (opt-in) | 10,000 | 16.209 ms | 41,176 | 693 |
| `Search` common (opt-in) | 100,000 | 166.276 ms | 36,376 | 691 |

Setup was excluded from those `ns/op` values. Representative setup metrics were
18.759 ms for 100 documents, 138.272 ms for 1,000 mixed documents, 67.762 ms for
250 PDFs, about 1.426 s for 10k, and 15.911 s for 100k. Index preparation is
therefore a material separate cost at larger sizes.

## Profile and query-plan observations

The CPU profile sampled 4,000 ms total CPU. SQLite's VDBE accounts for 14.25%
flat and 77.00% cumulative samples; B-tree movement is 16.50% cumulative and
the FTS5 BM25 function is 40.00% cumulative. This indicates
that query execution/ranking, not Go request setup, dominates this workload.
Profile sample totals are CPU samples, not wall-clock shares; they must not be
added as exclusive percentages.

The allocation profile aggregates the full 3-second run (261.78 MB allocated).
`fitSearchPage` accounts for 69.82% cumulative allocated space, including JSON
size-candidate serialization and cursor construction; `searchPageTx` accounts
for 23.32% cumulative. This locates allocation work but does not establish that
a replacement can preserve the exact 512 KiB response budget more cheaply.
Per-operation allocation figures in the table are the more direct steady-state
comparison. No production optimization was made: there is no measured before /
after production win to justify changing response sizing, query behavior, or
reader lifecycle.

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

## Correctness and limits

No production code, ranking weights, query grammar, schema, API, cursor limit,
or response cap changed. Existing Store and HTTP tests exercise stable tie order,
title-only PDF page 0, multi-page PDF attribution and provenance, advanced
positive/negative branches, cursor refresh fences, cancellation/Close cleanup,
the 1,000-result window, and result boundaries. The new synthetic performance
test additionally checks that `%` and `_` in path filters are literal and emits
the query plans above. Large fixture profiles are opt-in; profile artifacts are
local-only. No A/B binary series was run because this PR makes no production
change; the benchmark-only code is not claimed to improve user latency.
