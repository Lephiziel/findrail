# Indexing diagnostics (source builds)

Schema 6 stores only the most recent successful, committed source report. It is
written in the same SQLite transaction as the source inventory, metadata and
pruning. Failed scans do not replace it. An older schema-5 index has no report
until a successful scan; migration does not rescan files. Failed-attempt and
live progress history is not persisted.

For a failed local `index --json`, stdout returns `committed: false` plus a
bounded attempt summary with processed/skip counts and a stable `failure_code`;
`--show-paths` explicitly includes its single safe terminal failure path. Manual
jobs and watcher refreshes expose process-local attempt ID, sequence, phase,
partial processed/skipped counters, final commit state, and durable report ID on
success. These progress records reset on process restart; failures leave the
durable report in place.

```bash
findrail source-report --data-dir INDEX --source SOURCE_ID
findrail source-report --data-dir INDEX --source SOURCE_ID --json
findrail source-report --data-dir INDEX --source SOURCE_ID --show-paths --json
```

```powershell
findrail.exe source-report --data-dir 'C:\Findrail\Index' --source SOURCE_ID
findrail.exe source-report --data-dir 'C:\Findrail\Index' --source SOURCE_ID --show-paths --json
```

The command opens an existing index read-only: it does not create, migrate or
refresh one. HTTP `GET /api/v1/sources/{id}/report` omits example paths;
`?include_paths=true` explicitly requests the bounded examples. Unknown sources
return not found, while a registered source without a report returns an explicit
unavailable result. Archived sources report `frozen_origin_report_not_in_portable_v1`;
portable snapshot v1 is unchanged and no scan is started to fill this gap.

Reports distinguish indexed, updated, unchanged and removed documents from
observed files/entries/directories, skipped files/entries and pruned directories.
GitHub archive special entries use the `entry` unit; regular selected archive
files are counted as candidate files. Legacy `seen`/`skipped` counters
remain unchanged: filesystem `skipped` includes both file and directory policy
decisions, while report units keep them separate. For filesystem reports the
legacy aggregate reconciles as `skipped = skipped_files + skipped_entries +
pruned_directories`; GitHub selected-candidate `skipped` reconciles as skipped
files plus skipped special entries. Hidden or dependency directories
are one directory decision; their descendants are not enumerated to estimate
missing documents. GitHub coverage describes selected archive candidates only;
files outside a selected subdirectory are intentionally not represented as skips.

Stable reason codes include `excluded_path`, `hidden_entry`,
`dependency_directory`, `sensitive_name`, `symlink`, `special_entry`,
`office_lock_file`, `unsupported_format`, `format_disabled`, `input_too_large`,
`binary_or_non_utf8`, `extraction_limit`, `no_extractable_text`,
`unsupported_docx`, and `git_lfs_pointer`. They describe the first applicable
policy decision; subtype counters are not additional skips. Sensitive-name paths
are never sampled. Samples are examples, not an exhaustive list: at most 200
safe relative paths are retained and the serialized report is capped at 64 KiB.
No contents, absolute roots, URIs or raw OS error messages are stored.

For “a file is missing from search”, first check source freshness and the last
committed report. A disabled PDF/DOCX requires the existing source policy to be
configured and rescanned; an unsupported image or textless PDF is outside current
extraction support (OCR is not included). Size/extraction limits are policy caps,
not a promise that increasing them will support every format. Hidden, dependency,
sensitive and symlink exclusions are current policy. A report does not enumerate
every file, nor can it explain an arbitrary path that the scan never observed.

The report is a bounded local diagnostic view. Relative file names may still be
private; use `--show-paths` only when appropriate. The local index remains
plaintext. See [data handling](data-handling.md) and [source management](source-management.md).

## Synthetic resource spot check

One Linux amd64 run compared a clean-base binary with this instrumented binary
on 240 unsupported synthetic files plus one Markdown note: 7.0 ms vs 7.2 ms
wall time (one run each; startup and filesystem noise dominate). The instrumented
journey observed 246 filesystem entries (245 files), retained 200 of 243
skipped-file examples, and serialized the complete report to 17,090 bytes. This
demonstrates bounded output, not a stable overhead benchmark or a p95 claim.
Chromium 153 headless exercised the Sources report panel, hostile path text,
live progress, cancel and failed refresh with the old report retained, retry,
removal, Enter activation, and a 375px viewport. Visibility pause/resume is
covered by the browser-client lifecycle regression test (the available CDP
build has no page-visibility override command).
