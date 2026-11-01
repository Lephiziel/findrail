# Small Turkish and English search evaluation

This synthetic corpus exercises the current literal-word search; it is not a
relevance benchmark or a claim of semantic retrieval. Four fictional Markdown
documents live in `examples/evaluation/documents`. Ten labeled queries, including
three deliberate no-match cases, live in `examples/evaluation/queries.json`.
Expected paths are relative to the `documents` directory. AI assistance was used
to draft these labels and check them; independent human label review is still
welcome.

## Reproduce

From the repository root, build the CLI and index only the document directory:

```sh
go build -o bin/findrail ./cmd/findrail
bin/findrail index --data-dir .findrail-evaluation examples/evaluation/documents
bin/findrail search --data-dir .findrail-evaluation --json "kahve mercan"
```

Source builds also provide a read-only evaluation command. Copy the source ID
printed by `index` and run:

```sh
bin/findrail evaluate --data-dir .findrail-evaluation --source SOURCE_ID --queries examples/evaluation/queries.json --json
```

The command compares complete literal-search path sets for 1–100 cases and
prints a deterministic report with the exact match rate. Any mismatch returns
a nonzero exit status after the complete report. More than 100 matching
documents makes a case incomplete and returns an error instead of scoring a
truncated set. Evaluation reads an existing current-schema index; it does not
create or migrate one.

Do not index `examples/evaluation` itself: the JSON labels contain the query
words and would contaminate the results. Use a fresh dedicated data directory,
not an index containing personal sources. This evaluation does not need `serve`
or an account and does not upload documents. The index contains plaintext data.

For a manual comparison, pass each JSON entry's `query` to `search --json` and compare the sorted
`results[].path` set with `expected_paths`. Record the tested commit, OS, CLI
version, query, expected paths, observed paths, and whether they agree. Do not
commit generated databases or logs of private queries. If a case fails on a
later version, record it as an open limitation before changing the expectation.

## Observed baseline

Checked on 2026-10-02 using a source build of local alpha `0.1.0-alpha.1`,
commit `e8b8183`, Go 1.26.5 on macOS 27.0.1 arm64. An ordinary source build reports
`findrail dev`; it is not a downloaded release archive. The CLI indexed exactly
four documents. All ten path comparisons agreed:

| Query | Expected and observed paths |
| --- | --- |
| `kahve` | `kahve.md` |
| `kahve mercan` | `kahve.md` |
| `yürüyüş` | `yuruyus.md` |
| `zümrüt parkta` | `yuruyus.md` |
| `kahve zümrüt` | no match |
| `garden silver` | `garden.md` |
| `notebook copper` | `notebook.md` |
| `paper-prototype` | `notebook.md` |
| `note copper` | no match |
| `silver copper` | no match |

## What the cases establish

The accented Turkish queries use the same spelling as the documents. They
demonstrate those literal words working, not Turkish stemming, dotted/dotless-I
equivalence, or comprehensive accent normalization. `parkta` is the inflected
word stored in the note; this corpus does not assume that `park` matches it.

`notebook` is one token, so `note` does not match its prefix. The hyphen in
`paper-prototype` creates two AND-connected terms; it does not require a phrase
or adjacency. The cross-document marker pairs have no match because all query
terms must occur in the same document. No parser or retrieval code is changed.

See [alpha behavior](local-alpha.md) for the implemented search semantics and
the [three-document demo](demo.md) for PDF attribution and automatic refresh.
