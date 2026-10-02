# Integration and evaluation fixtures

Behavioural tests currently live beside the Go packages, including cross-module
tests in `internal/store/sqlite`, `internal/cli`, and `internal/transport/http`.
They create temporary synthetic sources rather than reading personal folders.

`examples/notes` is the shared demo corpus. `scripts/smoke.py` checks a compiled
binary through CLI and a real loopback HTTP listener with a temporary index.

Future retrieval evaluation belongs here: public or synthetic corpora, remembered
query tasks, expected source IDs / passages, and documented hardware. Keep
retrieval relevance and latency measurements distinct from correctness tests.
Do not add copied private documents or checked-in SQLite databases.
