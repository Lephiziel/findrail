# Manual evidence intake

`scripts/manual_evidence.py` is an offline maintainer utility. It reads only
explicit local JSON paths; it does not access the network, contact reporters, or
decide release readiness. The operator is responsible for voluntarily receiving
reports and checking their provenance. A report may remain private locally.

## Candidate and report files

`--candidate` must be the verified TASK24 strict candidate summary, not an
arbitrary draft. The intake rechecks the unchanged `readiness.strict_candidate`
contract (including source cleanliness, SHA, version, schema/API, assets, native
targets and archive identity). For alpha.5 use the candidate SHA from that
verified summary: the documentation integration commit `69a29ac...` is not the
published alpha.5 evidence SHA `cb53ee9...`.

Each `--report` is one UTF-8 JSON object with `kind`, `reference`,
`candidate_sha`, and `candidate_version`. A reference is a Findrail issue URL or
`sha256:<64 lowercase hex>` for a locally retained private source report. Never
publish the private report to establish its reference. References are
deduplicated exactly; distinct references, hashes, signatures, and operator
attestations do not establish distinct people. Do not collect names, email, or
device identifiers.

Manual first-run `kind` is `windows_first_run` or `apple_silicon_first_run` and
also requires `evidence_type: manual`, exact target (`windows-amd64` or
`darwin-arm64`), the actual `archive_sha256`, and all eight cases: `install`,
`launch`, `repeat-launch`, `retrieval`, `collection-restart`, `refresh`, `quit`,
`uninstall-data-preserved`. Each case has status `passed`, `failed`, or `not_run`,
nullable real `elapsed_seconds`, and optional short obstacle. TASK24 kit reports
use `id` for the case name and may include `observation`; both that shape and
the normalized `case` shape are accepted. The kit's top-level `status` is also
checked: `failed` remains failed even if its individual case entries look
passed, while `not_run` / `incomplete` remains incomplete. Optional kit identity
fields (schema/API and manifest/checksum hashes) are checked against the strict
candidate summary. Observer, date, notes, and observations are bounded input
only and are never exported. Unknown time stays `null`, never zero. Failed,
unrun, or unmeasured cases remain useful draft data but cannot become a passed
record.

Voluntary-use reports require `evidence_type: manual`, `return_use` boolean,
`retrieval_one` and `retrieval_two` (`found`, `not_found`, or `not_tried`), and
`collection_restart` (`restored`, `not_restored`, or `not_tried`). The report is
complete only when the person returned, tried both searches, and tried the
collection restart. `not_found` and `not_restored` are retained as real outcomes.
No query, document, or local path is needed.

Only allowlisted evidence fields go to `supplemental.json`; free-form obstacle
text and local input paths do not. JSON is capped at 1 MiB per file, usage intake
at 100 reports; duplicate keys, non-finite numbers, unknown fields, malformed
types, and invalid identities are rejected. Inputs are parsed as data and never
executed or rendered as HTML. Console output and `intake.json` contain only
fixed issue codes, counts, and safe references.

## Run

```sh
python3 scripts/manual_evidence.py \
  --candidate /absolute/path/verified-candidate.json \
  --report /absolute/path/report.json \
  --output /absolute/path/intake
```

Use a separate invocation and output directory for each kind. `intake.json`
reports `recorded`, `incomplete`, or `blocked`, with received/complete/
incomplete/failed/mismatch counts. It is not a beta or release decision. A
`supplemental.json` is written only when every supplied report is complete,
valid, unique, and for one kind and candidate. Any rerun removes a previous
supplemental file before validation, so a failed rerun cannot leave stale
evidence. Incomplete and invalid reports are counted, never silently filtered
into an apparent complete set. Keep test fixtures out of actual readiness runs.

An issue form is optional; a local private report is allowed. Publishing any
report is a separate voluntary human action. Native automated tests are not
manual first-run experience, and no human reports are claimed by this utility.
