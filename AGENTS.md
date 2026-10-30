# Repository guidance

Read README.md and docs/architecture.md before implementation. Roadmap scope and
shipped behaviour must remain distinct in documentation.

- Keep main small and compose dependencies in internal/cli.
- Put private implementation in internal; pkg/connector is experimental public API.
- Use Go by default for new development CLIs, binary/API integration tests,
  packaging, and archive / manifest / evidence validation. Reuse existing Go
  packages and standard-library facilities before adding another helper.
- Explain any Python-specific requirement and existing-tool reuse in the PR when
  introducing a Python tool or substantially expanding one. Existing Python
  tooling may receive focused correctness fixes; browser and client code should
  use the tools appropriate to those clients.
- Migrate tooling in bounded changes with equivalent boundary/error coverage,
  preserved report and archive contracts, and updated callers. Remove replaced
  helpers in the same PR; keep development tools separate from runtime commands.
- Preserve source provenance, bounded extraction, atomic scans, and rollback.
- Do not execute indexed content or add hidden runtime network / analytics calls.
- Do not widen loopback serving without implementing the documented auth design.
- Use parameterized SQL and safe text rendering.
- Format Go, run go vet, and run relevant tests. Storage / connector changes need lifecycle tests.
- Keep fixtures synthetic or public. Never commit indexes or credentials.
- Update schema migration documentation and tests together.
- Follow the user's authorized scope; avoid unnecessary approval steps.
