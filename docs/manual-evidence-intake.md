# Manual evidence intake

`cmd/findrail-manual` is an offline maintainer utility. It reads only
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

Manual first-run `kind` is `windows_first_run`, `apple_silicon_first_run`, or
`linux_first_run`; it also requires `evidence_type: manual`, exact target
(`windows-amd64`, `darwin-arm64`, or `linux-amd64`), the actual `archive_sha256`,
and all eight cases: `install`,
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
record. A blank Linux kit may leave `reference` empty and is classified
`incomplete`; it cannot produce readiness evidence.

Linux first-run intake is deliberately separate from readiness. A valid
Linux-only invocation writes `linux-observations.json`, never `supplemental.json`.
Its version-1 format contains the candidate identity and sanitized observations:

```json
{
  "format_version": 1,
  "kind": "linux_first_run",
  "candidate_sha": "<verified candidate SHA>",
  "candidate_version": "<verified candidate version>",
  "observations": [{
    "status": "incomplete",
    "target": "linux-amd64",
    "archive_sha256": "<matching archive hash>",
    "reference": "",
    "cases": [
      {"case": "install", "status": "not_run", "elapsed_seconds": null},
      {"case": "launch", "status": "not_run", "elapsed_seconds": null},
      {"case": "repeat-launch", "status": "not_run", "elapsed_seconds": null},
      {"case": "retrieval", "status": "not_run", "elapsed_seconds": null},
      {"case": "collection-restart", "status": "not_run", "elapsed_seconds": null},
      {"case": "refresh", "status": "not_run", "elapsed_seconds": null},
      {"case": "quit", "status": "not_run", "elapsed_seconds": null},
      {"case": "uninstall-data-preserved", "status": "not_run", "elapsed_seconds": null}
    ]
  }]
}
```

The real output has all eight ordered cases. It contains no free-form
observations. Complete and well-formed failed Linux reports are still standalone
feedback, not a new readiness kind or gate. `intake.json` counts `complete`,
`incomplete`, `failed`, and `mismatch`; a validated failure is retained with its
failed status and case outcomes, but makes intake `blocked`. Malformed,
mismatched, or duplicate-reference runs do not export report details and clear
prior supplemental/observation outputs.

Voluntary-use reports require `evidence_type: manual`, `return_use` boolean,
`retrieval_one` and `retrieval_two` (`found`, `not_found`, or `not_tried`), and
`collection_restart` (`restored`, `not_restored`, or `not_tried`). The report is
complete only when the person returned, tried both searches, and tried the
collection restart. `not_found` and `not_restored` are retained as real outcomes.
First use does not set `return_use: true`; set it only after actually returning
later. Skipped tasks stay `not_tried` and incomplete. No query, document, or
local path is needed. `VOLUNTARY_USAGE_TEMPLATE.json` is a separate blank draft,
not the first-run report. Valid incomplete voluntary reports produce a
sanitized `usage-observations.json` retaining these outcome facts, including a
well-formed report marked failed; such an intake remains blocked. A full matching
report continues through the existing readiness `supplemental.json` contract.
Unknown SHA feedback can still be qualitatively useful, but it cannot match a
candidate or count as release evidence.

Only allowlisted evidence fields go to `supplemental.json`; free-form obstacle
text and local input paths do not. Go parses each candidate/report from one
bounded byte snapshot (1 MiB per parser input); usage intake is capped at 100
reports. Duplicate keys, trailing JSON, non-finite/overflow numbers, unknown
fields, malformed types, and invalid identities are rejected. Beta handoff raw
reports retain their separate 8 KiB limit; it is not substituted for the parser
or batch bound. Transferable JSON output is capped at 8 MiB, with at most 4 MiB
of references in a batch summary. Inputs are parsed as data and never executed or
rendered as HTML. Console output and `intake.json` contain only fixed issue codes,
counts, and safe references.

## Run

```sh
go build -o /absolute/path/findrail-manual ./cmd/findrail-manual
/absolute/path/findrail-manual intake \
  --python "$(command -v python3)" \
  --readiness "$PWD/scripts/readiness.py" \
  --candidate /absolute/path/verified-candidate.json \
  --report /absolute/path/report.json \
  --output /absolute/path/intake
```

Use a separate invocation and output directory for each kind. `intake.json`
reports `recorded`, `incomplete`, or `blocked`, with received/complete/
incomplete/failed/mismatch counts and fixed output filenames. It is not a beta or
release decision. A readiness `supplemental.json` is written only when every
supplied Windows/Apple/voluntary report is complete, valid, unique, matching and
for one kind/candidate. Linux first-run and non-complete voluntary observations
use their separate sanitized outputs and are never readiness supplemental
records. Any rerun removes prior success files before validation, so a failed
rerun cannot leave stale evidence. Incomplete and invalid reports are counted,
never silently filtered into an apparent complete set. Keep test fixtures out of
actual readiness runs.

`findrail-manual validate` is the bounded single-report protocol used by beta
automation; it requires explicit `--python`, `--readiness`, `--candidate`, and
`--report` paths. It emits only fixed classification and sanitized outcomes. The
prebuilt tool is supplied explicitly; acceptance paths never invoke `go run` or
build a tool on demand. Beta callers use this same protocol for raw Linux intake,
incomplete Windows handoff, assemble, independent verify, and refresh. They pass
the original report file bytes to Go (not a Python-decoded/re-encoded object) and
use the original candidate summary byte snapshot for strict context validation.
Beta raw manual report files remain separately capped at 8 KiB; the Go parser's
1 MiB bound and the 100-report batch cap are unchanged.

An issue form is optional; a local private report is allowed. Publishing any
report is a separate voluntary human action. Native automated tests are not
manual first-run experience, and no human reports are claimed by this utility.
