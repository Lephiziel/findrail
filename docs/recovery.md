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
written atomically and must not contain source paths, indexed content, quotes,
or raw stderr. The Linux SIGKILL test covers process death before SQLite commit;
it does not establish power-loss safety or equivalent behavior on other OSes.
Packaged-candidate journeys must separately identify the exact supplied binary
revision and schema, and must not silently build a second application binary.
