# Contributing

Findrail is an early open-source project. Start with the runnable demo, the
product specification, architecture, and implementation plan. Work that improves
the real retrieval workflow is welcome.

## Development

Use Go 1.26 or newer. Run `go mod download`, `go test ./...`, `go vet ./...`, and
`go test -race ./...` on a supported race-detector platform. Format Go with
`gofmt`. CI checks multiple operating systems and builds without CGO.

## Pull requests

Describe the user-visible problem, resulting behaviour, and relevant validation.
Keep source fixtures synthetic or public; never include private documents,
tokens, database files, or real query logs. Include lifecycle or boundary tests
for storage / connector changes and an upgrade plan for schema changes.

Small fixes can be proposed directly. For a new connector, retrieval engine,
dependency family, or storage migration, open a design discussion to establish
the contract before investing in a large implementation.

## Useful areas

Extraction fixtures, Unicode cases, source conformance, search evaluation,
accessibility, packaging, and clear documentation. Planned modules have scoped
acceptance criteria under `docs/implementation-plan.md`.

## License

By contributing, you provide your contributions under the repository's
Apache-2.0 license. Include necessary attribution for externally sourced code.
No contributor license agreement is currently required.
