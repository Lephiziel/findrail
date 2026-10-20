# Contributing

Findrail is an open-source local-first search project. For current behaviour,
start with the runnable demo and architecture; the roadmap distinguishes shipped
work from planned work.

Try the [three-document demo](docs/demo.md), then choose a scoped item from
[current contributor tasks](docs/contributor-tasks.md) or browse the live
[good first issues](https://github.com/Lephiziel/findrail/issues?q=is%3Aissue%20is%3Aopen%20label%3A%22good%20first%20issue%22)
and [help wanted](https://github.com/Lephiziel/findrail/issues?q=is%3Aissue%20is%3Aopen%20label%3A%22help%20wanted%22).
Check the issue for current status and recent discussion; a short comment with
your intended scope helps avoid duplicate work, but no assignment or coordinator
approval is required for a small contribution. Non-code feedback is welcome too.

## Development

The module requires Go 1.26 or newer. CI checks Go 1.27.x on Linux, macOS and
Windows, builds without CGO, and runs the race detector on Go 1.26.x/Linux. After
cloning, run the repository's existing check target:

```sh
make check
```

This runs `gofmt` validation, `go vet ./...` and `go test ./...`. For changes to
concurrent or lifecycle-sensitive code, also run `make race` where the platform
supports it. Run focused package tests while iterating, for example
`go test ./internal/extract/docx`; see CI for the complete platform matrix. There
is no need to download modules separately: Go resolves the declared module graph.

## Pull requests

Describe the user-visible problem, resulting behaviour, and relevant validation
(including which checks you ran).
Keep source fixtures synthetic or public; never include private documents,
tokens, database files, or real query logs. Include lifecycle or boundary tests
for storage / connector changes and an upgrade plan for schema changes.

Small fixes can be proposed directly. For a new connector, retrieval engine,
dependency family, or storage migration, open a design discussion to establish
the contract before investing in a large implementation.

## Useful areas

Connector authors can follow the [conformance kit workflow](docs/connector-conformance.md).

Extraction fixtures, Unicode cases, source conformance, search evaluation,
accessibility, packaging, and clear documentation. Planned modules have scoped
acceptance criteria under `docs/implementation-plan.md`.

## License

By contributing, you provide your contributions under the repository's
Apache-2.0 license. Include necessary attribution for externally sourced code.
No contributor license agreement is currently required.
