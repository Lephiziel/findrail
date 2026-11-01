# ADR 0006: public GitHub snapshot integrity

Status: accepted, 2026-10-06.

Findrail indexes files from public `github.com` repositories only. It resolves a
default branch, explicit ref, or pinned commit to a full commit SHA, downloads
one tar archive by that SHA, and validates the archive before opening a write
transaction. PAX metadata is accounted for separately from real archive roots;
the effective names produced by PAX/GNU headers are validated. No token, Git
executable, hooks, or repository code is used.

The preparation budgets are: metadata 2 MiB, compressed response 32 MiB,
expanded gzip stream 128 MiB, retained text 32 MiB, 50,000 entries, 5,000
documents, and 2,048 bytes per effective path. The 1 MiB default and 8 MiB
maximum per-file text limits are applied independently. The entire decompressed
tail is checked for zero padding, checksum/truncated trailers are verified, and
additional inventories or gzip members are rejected.

Repository identity is resolved before archive download. The initial
registration token/revision (or absence) is passed to the atomic publish CAS;
forget, a newer snapshot, and concurrent initial registrations therefore cannot
be overwritten by an older download. Reads of source counts and GitHub metadata
use one SQLite snapshot.

GitHub configuration and the published SHA live in a schema-3
`github_sources` table. Search, preview, citations, HTTP, folder watching, and
MCP only read the local snapshot. Private repositories, credentials, automatic
remote polling, issues, discussions, and resumable sync remain future work.
