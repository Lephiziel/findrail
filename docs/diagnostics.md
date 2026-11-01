# Diagnostics and support report

`findrail doctor` performs bounded, offline inspection. It never opens the writer,
repairs the database, or runs a migration. Its JSON format remains
`report_version: 1`; `index_schema` is the actual SQLite `user_version`, while
`current_write_schema` is the schema this binary writes. A newer schema is an
error. An older schema is reported as needing a backed-up upgrade; doctor does
not promise that every intermediate version can be read. The ordinary read-only
store and the special schema-5 source-report profile are separate contracts.
An absent data directory or index is a normal first-run state and is not created
by doctor. `--show-paths` is an explicit CLI-only opt-in.

For an optional bounded SQLite inspection, run
`findrail doctor --data-dir INDEX --integrity --json`. This adds separate
structural quick-check and foreign-key checks only for the current schema. The
whole command shares a five-second deadline and opens the existing index
read-only; it never creates an index or repairs/rebuilds anything. Findings are
limited and database-controlled details are not included. Older schemas are
skipped and retain the normal upgrade warning. A successful quick check covers
only part of SQLite structural integrity; it does not establish UNIQUE/index
entry consistency, full FTS/content consistency, search quality, or that all data
is recoverable. See [SQLite quick_check](https://www.sqlite.org/pragma.html#pragma_quick_check)
and [foreign_key_check](https://www.sqlite.org/pragma.html#pragma_foreign_key_check).
Result-reading failures, cancellation, and stopping at the finding limit are
reported as incomplete, never as a completed scan or a healthy index.

The web UI offers an explicit **Diagnostics** dialog when the backend advertises
the capability. The report is generated locally and is limited to 16 KiB. It
contains application and Go/platform versions, actual/current schema, safe check
codes, and aggregate source/policy counts. It never includes filesystem paths,
source/document names or IDs, indexed content, search queries, credentials,
environment, command arguments, logs, cookies, or raw operating-system/database
errors. The browser downloads `findrail-diagnostics.json` only after the user
chooses **Download report**. There is no report upload or remote telemetry.

The endpoint uses the existing local read backend and retains the server's
loopback, Host and Origin checks. It does not open a writer or trigger indexing.
If inspection is unavailable, the UI offers retry and does not retain a stale
report after the dialog closes.
