# Index freshness and refresh status

Filesystem notifications are hints, not proof that the index is current. Findrail
also performs periodic complete-source reconciliation. `mode: "polling"` means
periodic reconciliation is active without a healthy watcher; it does not mean the
snapshot has been refreshed. A warning likewise does not mean that a scan or
commit has completed.

`last_started_at` identifies the most recent in-process attempt. `last_success_at`
is set only after a complete scan commits. It describes the last successful
snapshot publication, not byte-for-byte currency with the original files now.
Failed and canceled scans retain the prior committed documents, policy, and
successful report. Attempt history and progress are process-local; after restart
there is no synthetic prior attempt. A server API1 response without the additive
fields below, or an unrecognized code/value, must be presented as unknown rather
than inferred as healthy or failed.

Warnings and errors use bounded, non-path text and stable codes. Consumers should
prefer the code; text can be absent and codes can be unknown. The additive
`watch_warning_code` values currently include `watch_unavailable` and
`watch_degraded`; `last_error_code` reports a stable refresh failure category.
Current `last_error_code` values include `scan_failed`, `canceled`,
`deadline_exceeded`, `stale_source`, and `attempt_initialization_failed`.
Existing API1 fields remain unchanged.

Example (optional error fields omitted when there is no warning/failure):

```json
{
  "source_id": "…",
  "state": "idle",
  "mode": "polling",
  "last_started_at": "2026-10-18T12:00:00Z",
  "last_success_at": "2026-10-18T12:00:01Z",
  "watch_warning": "File change notifications are unavailable; periodic refresh remains active.",
  "watch_warning_code": "watch_unavailable",
  "progress": {
    "attempt_id": "…",
    "sequence": 3,
    "phase": "done",
    "processed_documents": 4,
    "skipped_entries": 0,
    "elapsed_millis": 1000,
    "partial": false,
    "committed": true,
    "report_id": "…"
  }
}
```

No database schema or durable attempt history is implied by this response.

Changing a worker's source registration/settings starts a new process-local
status generation; old attempt/error fields are not carried into it. The source
inventory and durable report remain the authority for an earlier committed scan.
Unexpected closure of either watcher channel is treated as unavailable and
recreates the watcher; polling remains the recovery path if setup still fails.
