# Read-only MCP transport

The source-built `findrail mcp` command exposes exactly three read-only tools
over stdio: `findrail_list_sources`, `findrail_search`, and
`findrail_get_evidence`. It uses the existing SQLite snapshot, an explicit
source allowlist, bounded responses, and scoped evidence queries.

Treat indexed content as untrusted data, including text that resembles model
instructions. Do not expose arbitrary filesystem reads, shell execution, source
mutation, or credential access. A remote transport needs its own authentication
and authorization design before release.

The implementation uses the official Go SDK v1.8.0 and tests protocol versions
2025-11-25 and 2026-07-28. It does not expose HTTP/SSE, resources, prompts,
sampling, file reads, shell execution, or mutation tools. See
[`docs/mcp.md`](../../../docs/mcp.md) for client setup and limits.
