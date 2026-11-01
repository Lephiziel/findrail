# GitHub snapshot demo (20–30 seconds)

```bash
go build -o bin/findrail ./cmd/findrail
mkdir -p /tmp/findrail-demo-notes
printf 'A committed snapshot keeps provenance reproducible.\n' > /tmp/findrail-demo-notes/note.md
./bin/findrail index --data-dir /tmp/findrail-github-demo /tmp/findrail-demo-notes
./bin/findrail index-github --data-dir /tmp/findrail-github-demo --path docs Lephiziel/findrail
./bin/findrail sources --data-dir /tmp/findrail-github-demo --json
./bin/findrail serve --data-dir /tmp/findrail-github-demo
```

Choose a word present in both sources; adjust the note if the repository changes.

- 0–5s: show one local and one GitHub result.
- 5–10s: select the GitHub filter and open its cached preview.
- 10–17s: use **Copy as Markdown** and show the full commit SHA.
- 17–24s: with GitHub unavailable, reopen the preview; briefly show the manual
  refresh command or put it in a second clip.
- End: “Build Findrail from source and try public GitHub snapshots.”

Factual announcement points: mixed-source keyword search; offline indexed
preview; commit-pinned citations. Do not claim semantic or AI search.
