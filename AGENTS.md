# Repository guidance

Read README.md and docs/architecture.md before implementation. Roadmap scope and
shipped behaviour must remain distinct in documentation.

- Keep main small and compose dependencies in internal/cli.
- Put private implementation in internal; pkg/connector is experimental public API.
- Preserve source provenance, bounded extraction, atomic scans, and rollback.
- Do not execute indexed content or add hidden runtime network / analytics calls.
- Do not widen loopback serving without implementing the documented auth design.
- Use parameterized SQL and safe text rendering.
- Format Go, run go vet, and run relevant tests. Storage / connector changes need lifecycle tests.
- Keep fixtures synthetic or public. Never commit indexes or credentials.
- Update schema migration documentation and tests together.
- Follow the user's authorized scope; avoid unnecessary approval steps.
