# ADR 0005: public GitHub snapshots

Status: accepted, 2026-10-06.

Findrail indexes files from public `github.com` repositories only. It resolves a
default branch, explicit ref, or pinned commit to a full commit SHA, downloads one
tar archive by that SHA, and validates bounded metadata, compressed, expanded,
entry, document, retained-text, and path budgets before opening a write
transaction. No token, Git executable, hooks, or repository code is used.

GitHub configuration and the published SHA live in a schema-3 `github_sources`
table. Source identity combines numeric repository ID, ref-selection mode/value,
and selected directory; it excludes the current SHA and extraction limit. A
registration token and revision prevent stale refreshes and resurrection after
forget. Documents, deletions, metadata, SHA, and success time publish atomically.

GitHub refresh is manual. Search, preview, citations, HTTP, folder watching, and
MCP only read the local snapshot. Private repositories, credentials, automatic
remote polling, issues, discussions, and resumable sync remain future work.
