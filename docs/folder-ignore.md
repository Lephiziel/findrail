# Folder ignore rules (source builds)

A regular UTF-8 `.findrailignore` directly in an explicitly selected filesystem
root adds exclusions for that root. Findrail reads but never creates or edits it.
It is root-only: nested files, `.gitignore`, Git configuration, parent folders,
home-directory settings, GitHub sources and imported archives do not participate.
The file itself is always hidden from indexing. Rules cannot override hidden,
dependency, sensitive-name, symlink/special-file, index-directory, format,
encoding, size or extraction limits.

The language is intentionally smaller than Git's. Lines use LF or CRLF; one
initial UTF-8 BOM is allowed. Empty lines and first-character `#` lines are
comments. `!` negates an earlier user exclusion. `\#` and `\!` quote a literal
first character. Other backslashes, classes, continuation, controls, absolute
paths, traversal and malformed patterns are errors. Unescaped trailing ASCII
spaces are ignored; leading/internal spaces are significant. A literal filename
ending in a space cannot be named exactly by this initial syntax (a wildcard may
still match it).

`/` separates segments. A leading slash anchors to the root; a middle slash
makes the pattern root-relative; a pattern without slashes matches a basename
at every depth. A trailing slash matches directories only. `*` and `?` match
within one segment; `**` is a complete segment matching zero or more segments.
Last matching user rule wins. Comments occupy their own lines; there is no
inline-comment syntax. For example:

```text
# Basename at any depth
*.log
# Root-level directory only
/scratch/
# Zero or more intermediate directories
notes/**/draft-?.md
/notes/*
# Keep this file, but not a pruned parent
!/notes/keep.md
\#literal.md
\!literal.md
```

An excluded directory is pruned; a later descendant negation cannot recover
it unless its parent remained traversable. Rules are case-sensitive on every
OS. Limits: 64 KiB file, 256 rules, 1,024 UTF-8 bytes / 64 segments per rule,
4,096 bytes / 256 segments per checked path, and 4,096 compiled pattern
segments total. Matching uses dynamic programming, not recursive glob
backtracking. Each decision charges at most 16 Mi byte-product work units
(pattern bytes × path bytes per considered rule); exceeding the budget fails
the scan/explanation safely rather than silently treating a path as eligible.

Rule edits do not immediately erase indexed text. The previous searchable
snapshot remains until a complete scan succeeds; malformed, unreadable or
changing policy fails the scan and preserves the old snapshot/report. A
successful exclusion scan logically prunes documents, not cryptographically
erases disk/WAL bytes. Filesystem consistency is optimistic and bounded, not a
global filesystem lock. Watchers reconcile root policy changes; periodic scans
remain the recovery fallback.

Preview a bounded set of metadata-only decisions without opening the index:

```sh
findrail explain-path "/path/to/notes" --path notes.md --path build/generated.md
findrail explain-path "/path/to/notes" --path build/generated.md --json
findrail explain-path "/path/to/notes" --path build/generated.md --show-rule
findrail explain-path "/path/to/notes" --path private-index/cache.md --data-dir "/path/to/notes/private-index"
```

The JSON format has `version: 1`; status codes describe path-policy/metadata,
not actual indexing. PDF/DOCX source-specific settings, encoding, extraction
success and committed presence cannot be inferred. `--data-dir` supplies only
the path for the dedicated index-directory hard exclusion; the index is never
opened. Without it, the default data directory is used. `--show-rule` explicitly
includes a matched rule; normal diagnostics never persist rule text or document
contents. The standalone alpha.4 release includes this policy; the older alpha.2 release did not. The recipes below were checked with the stated source build.

## Optional recipes

These are examples for fictional sources, not default exclusions. Put the file
at the root you explicitly select. Names and matching are case-sensitive.
Hidden entries, sensitive filenames, symlinks and dependency directories remain
excluded even with a negation rule; `.obsidian/` needs no user rule. Excluding a
directory prunes it, so a later `!` for a child cannot bring that child back.

### Notes vault

Suppose this root contains:

| Path | Purpose |
| --- | --- |
| `Templates/template.md` | A reusable template |
| `Archive/old.md` | An archived note |
| `Notes/current.md` | A current note |
| `Notes/keep.md` | Another current note |

Optional root `.findrailignore`:

```text
/Templates/
/Archive/
```

```sh
findrail explain-path "/path/to/fictional-notes" --path Templates/template.md --path Archive/old.md --path Notes/current.md --path Notes/keep.md --json
```

| Path | Expected decision |
| --- | --- |
| `Templates/template.md` | `blocked_by_pruned_ancestor` (`Templates`) |
| `Archive/old.md` | `blocked_by_pruned_ancestor` (`Archive`) |
| `Notes/current.md` | `eligible_by_path_policy` |
| `Notes/keep.md` | `eligible_by_path_policy` |

### Go repository

Suppose this root contains:

| Path | Purpose |
| --- | --- |
| `README.md` | Project documentation |
| `cmd/app/main.go` | Hand-written code |
| `internal/generated/types.go` | A chosen generated directory |
| `internal/api/service.pb.go` | Generated protocol code |
| `internal/api/service.gen.go` | Other generated code |

Optional root `.findrailignore`:

```text
*.pb.go
*.gen.go
/internal/generated/
```

```sh
findrail explain-path "/path/to/fictional-go" --path README.md --path cmd/app/main.go --path internal/generated/types.go --path internal/api/service.pb.go --path internal/api/service.gen.go --json
```

| Path | Expected decision |
| --- | --- |
| `README.md` | `eligible_by_path_policy` |
| `cmd/app/main.go` | `eligible_by_path_policy` |
| `internal/generated/types.go` | `blocked_by_pruned_ancestor` (`internal/generated`) |
| `internal/api/service.pb.go` | `excluded_user_rule` |
| `internal/api/service.gen.go` | `excluded_user_rule` |

A basename pattern such as `*.pb.go` applies at every depth; the leading slash
on `/internal/generated/` anchors that directory to this root. These rules do
not implement all of `.gitignore`.

### Verification

On 14 October 2026, all nine observed JSON decisions matched the tables above
using `findrail dev`, built with Go 1.26.5 on macOS arm64 from source commit
`4746d61c17fe5e097db7d43e28d4a80b139091fd`. Each fixture was created in a temporary
folder, with regular UTF-8 files and only the listed root rules. No index was
opened. `eligible_by_path_policy` is a metadata decision, not a promise that
content extraction or indexing will succeed. The macOS arm64 standalone `findrail v0.1.0-alpha.4` binary from the
GitHub release was also checked after verifying its archive against the published
SHA256SUMS; all nine decisions matched. Other operating systems were not tested.

## Matcher resource spot check

One Linux amd64 run with Go `go1.27.2` ran
`go test ./internal/ignorepolicy -run '^$' -bench BenchmarkNearLimitMatch -benchtime=1x -benchmem`:
64 active rules (64 segments each, 4,096 compiled segments, about 12 KiB input)
against a 4,095-byte, 256-segment path took 466,173 ns and allocated 445,856 bytes
(4,055 allocations).
This is one bounded-work spot check, not p95, scale certification or a security
sandbox claim.

GitHub remains its bounded public text snapshot and archives remain frozen
imports. Neither uses these filesystem rules; portable v1 is unchanged. See
[connector behavior](connectors.md) and [local refresh semantics](local-alpha.md).
