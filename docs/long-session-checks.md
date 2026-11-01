# Long-session mixed-load checks

`scripts/mixed_load_soak.py` exercises a prepared Findrail executable in a new
temporary source and data directory. It uses loopback HTTP and the existing
`start`, `index`, `forget`, paging, saved-collection, and read-only doctor
contracts; it does not build a binary, use a user data directory, repair an
index, or call external services.

```sh
python3 scripts/mixed_load_soak.py --binary /abs/candidate/findrail \
  --profile short --seed 42 --output /abs/evidence/soak-short.json
python3 scripts/mixed_load_soak.py --binary /abs/candidate/findrail \
  --profile long --duration-seconds 1800 --seed 42 \
  --output /abs/evidence/soak-long.json
```

Short runs three cycles under a 180-second wall limit. Long defaults to 1,800
seconds (maximum 3,600), and a passing result requires at least twelve complete
cycles and the requested elapsed duration. Corpus size is 100 by default or
1,000 with `--documents`; concurrency defaults to four. The report records the
binary SHA-256 and doctor-reported version/revision, source revision, selected
workload, operation counts, cycle/restart counts, elapsed time, database/WAL
sizes, and available Linux process RSS/FD samples. RSS is sampled Findrail
process memory, not Python memory, and is not proof that no leak exists.

Every cycle concurrently searches, walks signed result pages while checking
count/order/uniqueness, edits/adds/deletes fixture documents and refreshes,
performs a separate canceled management refresh and checks that the committed
baseline remains searchable, saves a citation and reopens its quote/provenance
after an explicitly recorded process restart, and runs `doctor --integrity`
outside the writer process. Every third cycle forgets and re-adds a separate,
isolated synthetic source, verifying its absent and restored search states.
Cursor errors caused by a changed generation are fences; the harness accepts
only `cursor_stale` or `cursor_session_changed` for its saved pre-refresh cursor
and never continues a page sequence across a refresh. Long mode paces repeated
cycles with a bounded one-second inter-cycle pacing until its selected wall duration; it does
not substitute an idle delay after a fixed set of fast cycles. Unexpected failures produce a nonzero exit
and bounded JSON with stable failure codes, without indexed text, arbitrary
exception details, tokens, or temporary absolute paths. SIGINT produces an
incomplete report. The report is operational evidence, not readiness
`voluntary_usage` evidence.

## Observed facts

Only the values in a generated report are observations for that exact binary,
source SHA, corpus, host, and run. A successful synthetic run establishes these
specific automated invariants for that run only.

## Not established

This harness does not establish absence of memory/resource leaks, day-long
human use, behavior after returning the next day, native GUI/Finder/Windows
experience, or an independent user review. These remain hypotheses or require
separate evidence; synthetic output must not be presented as those claims.
