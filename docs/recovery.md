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

Run the separate executable journey with an extracted candidate; it never builds
or locates a binary on its own:

```sh
python3 scripts/recovery_candidate.py \
  --binary /path/to/extracted/findrail \
  --revision CANDIDATE_REVISION --schema 8 \
  --work-dir /tmp/findrail-recovery-work \
  --report /tmp/findrail-recovery-report.json
```

It verifies or computes the binary SHA-256, creates synthetic source and data
directories, seeds the index with that exact executable, then interrupts a
bounded refresh process group. Its precise commit point is not controllable, so
the report marks commit as unknown and accepts only a semantically consistent
old or new complete snapshot. An early exit, deadline, failed doctor, or mixed
snapshot is not a pass. The report contains only case/status/duration/phase,
binary SHA-256, caller-supplied revision/schema and platform; command output,
paths and indexed content are excluded. The synthetic work directory is left
for diagnosis and is never deleted by the harness.

This candidate journey covers the ordinary filesystem refresh path only. The
configure, collection-import, and Forget lifecycle guarantees are checked by
the deterministic production helper-process suite, not by claims about a
packaged application's management UI.
