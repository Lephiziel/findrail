# Read-only MCP design

Planned; no MCP endpoint or tools exist in this foundation.

Begin with `search` and `get_evidence` tools over stdio, using the same retrieval
contracts as CLI and HTTP. Each request has a bounded query / result budget and
an explicit source allowlist. Evidence includes source identity, original URI,
passage location, and freshness.

Treat indexed content as untrusted data, including text that resembles model
instructions. Do not expose arbitrary filesystem reads, shell execution, source
mutation, or credential access. A remote transport needs its own authentication
and authorization design before release.

Choose protocol dependencies from the official specification when this milestone
starts; the folder does not claim compatibility with a future protocol version.
