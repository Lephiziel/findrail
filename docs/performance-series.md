# Repeated search performance series

`scripts/resource_series.py` orchestrates the existing fixture-v2 resource check;
it does not rebuild or fetch binaries and does not change fixture generation,
query limits, readiness classification, or the regular report schema. The optional
`--samples-report` sidecar records numeric HTTP latency samples for the fixed
`common` and `rare` synthetic families only. It excludes and declares each
family's first request. Percentiles use nearest rank (`ceil(p*n)`, one-based).
The ordinary report is unchanged when the sidecar option is omitted.

Prepare both binaries in advance. Example full series (large run is intentionally
opt-in):

```sh
python3 scripts/resource_series.py --binary /abs/candidate/findrail \
  --baseline /abs/reference/findrail --documents 10000 100000 \
  --repeats 3 --samples-per-family 100 --seed 42 --allow-large \
  --deadline-seconds 2700 --output /abs/evidence/series
```

The short contract check can use `--documents 100 1000`; 100k requires
`--allow-large`. Limits remain three repeats, 100 warm requests per family and
the resource check's existing maximum of 100 queries and 1,800-second child
deadline. The suite wall deadline defaults to 2,700 seconds and is capped at
3,600. Runs use separate child-created temporary source/index data directories.
The balanced two-binary sequence is candidate/reference, reference/candidate,
candidate/reference (AB, BA, AB). This is warm-process HTTP latency; OS page cache
is not cleared and is not represented as cold/reset. RSS remains the underlying
harness's sampled Findrail process maximum, not a guaranteed process peak.

`series.json` preserves per-run report and samples paths relative to the output
directory and SHA-256 hashes. Family summaries report every run's p50/p95,
median/min/max run p95 and pooled p95 from only that size/family/role's raw
samples. Common and rare are never pooled together. Paired ratios are descriptive,
not pass/fail thresholds. Slow but complete measurements can pass operationally;
performance assessment is separate. Missing samples remain incomplete with
counts/reasons, not silently dropped from a claimed complete series.

Binary hash is verified and recorded. The harness does not infer compiler flags,
dirty source or build provenance from executable bytes; unavailable metadata is
marked explicitly. Unless independent metadata establishes matching compiler,
flags, source/corpus, and hardware, differences are `reference_only`, not proof
of a source optimization. No version/build command is run against arbitrary
binary metadata. No request text, document paths or content is retained.

The output directory budget is 16 MiB and child concurrency is one (maximum
allowed six); stderr is redirected to temporary files and only a bounded digest
is retained on child errors. On POSIX, process groups are terminated, then killed
and reaped on timeout/interrupt. On non-POSIX hosts only the direct child can be
terminated by this script; descendant cleanup is not guaranteed there. A child
timeout, failure, interruption, malformed child report, or budget overrun returns
nonzero and records bounded fixed diagnostics. Large samples are local evidence;
do not commit raw profiles or logs.

The series complements rather than replaces the existing individual scale record.
`readiness.check_scale` continues to accept only its original schema-1 report and
fixed fixture hashes; a series is not readiness evidence and does not imply
`p95 < 150 ms`.
