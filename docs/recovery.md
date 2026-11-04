# Index recovery

Findrail commits source scans, source configuration, snapshot imports, and source
removal as SQLite transactions. After an abrupt process stop, reopen the data
directory with a compatible Findrail binary and verify its state. SQLite may
recover or change `-wal` / `-shm` files while reopening; byte-for-byte equality of
those files is not expected.

## If Findrail stops unexpectedly

1. Stop **all** Findrail processes that might be using the index. Do not copy or
   inspect a live data directory as though it were a consistent backup.
2. Preserve a complete copy of the entire data directory, including
   `findrail.db` and any SQLite sidecar files. Work on a separate copy; do not
   overwrite the original.
3. Run `findrail doctor --data-dir <copy>` without repair options. Doctor is an
   inspection, not a repair or migration command. Keep its report private if it
   contains machine-specific details.
4. If the index is corrupt or cannot be opened, preserve it unchanged for
   diagnosis. Do not delete it and allow Findrail to create a replacement at
   that path.
5. To return to a pre-upgrade state, restore the **complete** backup made while
   Findrail was stopped into a new, separate directory and open that copy with
   the compatible older binary. Never point an older binary at the upgraded
   original index.

There is no automatic database repair or backup command. A backup must include
the whole data directory and must be taken while every Findrail process is
stopped.

## Crash-test evidence contract

Process recovery tests emit a bounded JSON record with `case`, `status`,
`duration_ms`, `phase`, test-binary SHA-256, build revision, schema, and platform.
The helper-process test binary hash identifies the helper, **not** a packaged
Findrail candidate. `passed` is the only successful status; failed,
interrupted, and unexecuted cases must not be counted as passes. Reports are
written atomically to the system temporary directory, or to
`FINDRAIL_RECOVERY_REPORT_DIR` when set, and must not contain source paths,
indexed content, quotes, or raw stderr. The Go helper cases use deterministic
barriers around refresh/prune, Configure, populated evidence-bundle import, and
Forget. They are production package tests, not packaged application tests.
Refresh and Configure are tested before publication and after success; bundle
import before publication and after success; Forget before statement commit and
after success. Reopen checks cover FTS, source/report consistency, registration
guards, PDF page identity, cursor invalidation, SQLite/FK integrity, collection
order, notes, offsets, source-linked cascades, and unrelated collection
preservation. Test-only SQLite triggers provide barriers inside bundle and
Forget writes; no production fault endpoint is introduced. Linux SIGKILL does
not establish power-loss safety or equivalent behavior on other OSes.

## Supplied-candidate journey (Linux)

Build the development-only producer from the source checkout, then run the
separate executable journey with an explicitly extracted candidate. The helper
never builds or locates the app binary itself:

```sh
go build -o /tmp/findrail-resource-check ./cmd/findrail-resource-check
mkdir -p /tmp/findrail-recovery-work
/tmp/findrail-resource-check sigkill \
  --binary /path/to/extracted/findrail --binary-sha256 CANDIDATE_SHA256 \
  --revision CANDIDATE_REVISION --schema 8 \
  --work-dir /tmp/findrail-recovery-work \
  --report /tmp/findrail-recovery-report.json --deadline 45
```

The `--work-dir` parent must already exist and be owned by the caller. The
producer creates a unique private child directory beneath it.

It verifies or computes the binary SHA-256, creates synthetic source and data
directories, seeds the index with that exact executable, then interrupts a
bounded refresh process group. Its precise commit point is not controllable, so
the report marks commit as unknown and accepts only a semantically consistent
old or new complete snapshot. An early exit, deadline, failed doctor, or mixed
snapshot is not a pass. The report contains only case/status/duration/phase,
binary SHA-256, caller-supplied revision/schema and platform; command output,
paths and indexed content are excluded. The synthetic work directory is left
for diagnosis and is never deleted by the harness.

The deadline bounds the whole candidate journey (at most 120 seconds), including
setup, commands, observation, reopen and checks. The runner bounds captured
stdout/stderr and output size, and kills only the owned process group, including
descendants after leader exit. SIGINT/SIGTERM writes an `interrupted` record with
the last phase; failed runs replace any earlier passed record. The old-or-new
check reads all four totals for the updated, added and pruned documents.
The producer keeps exception details private and emits only fixed
`failure_code` classifications: preflight `unsupported_platform`,
`invalid_identity`, `invalid_deadline`, `invalid_binary`,
`binary_hash_mismatch`, `invalid_work_dir`, and `proc_unavailable`; run phases
`setup_failed`, `seed_failed`, `seed_verification_failed`, `child_start_failed`,
`early_exit`, `observation_timeout`, `doctor_failed`, `doctor_invalid_json`,
`doctor_identity_mismatch`, `integrity_failed`, `search_failed`,
`mixed_snapshot`, and `binary_changed`; cancellation is `interrupted`. These
replace Python exception-class names with privacy-safe stable codes; no consumer
acceptance rule is weakened.

The same development executable offers `portable-boundary --binary PATH`. It
indexes a deterministic 8 MiB synthetic ASCII file and exports that source from
an isolated temporary index, reporting archive size, elapsed seconds and observed
peak child RSS (or JSON `null` when `/proc` did not provide a measurement). This
is a single-document boundary spot check, not a scale or leak benchmark. Its
reported platform and Go runtime identify the producer environment; historical
Python evidence is retained unchanged.

This candidate journey covers the ordinary filesystem refresh path only. The
configure, collection-import, and Forget lifecycle guarantees are checked by
the deterministic production helper-process suite, not by claims about a
packaged application's management UI.
