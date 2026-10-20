# Roadmap

This roadmap describes product scope and release gates. It is not a promise of
dates. Status changes require working code and evidence.

| Stage | Outcome | Release gate | Status |
|---|---|---|---|
| R00 · Foundation | Local index, keyword search, CLI, browser UI, contracts, CI | Lifecycle / rollback tests and a runnable demo | Implemented; local validation recorded separately |
| R01 · Local alpha | PDF text, bounded DOCX body snapshots, watcher, source diagnostics, folder ignore rules, preview and evidence export | Lifecycle checks and read-only literal evaluation; broader real-user retrieval validation | Published alpha.5 includes freshness, paging, passage windows, and recovery work. Broader independent retrieval validation remains open |
| R02 · Connected search | Read-only GitHub, local + remote combined search, secure credentials | Resume, rate limit, revocation, and deletion tests | Public file snapshots/manual refresh and web management are published; private repositories and resumable sync remain planned |
| R03 · Extensions | Versioned connector SDK, examples, conformance harness | Independent contributor creates an adapter from docs | Experimental conformance kit and separate-module example implemented; independent-contributor gate and API stabilization remain open |
| R04 · Optional semantics | Passage chunking, opt-in embedding backend, hybrid ranking | Better retrieval on labeled corpus without losing exact-match quality | Planned |
| R05 · Daily access | Native desktop launcher; read-only VS Code client ([setup](vscode.md)); read-only MCP | Single-instance lifecycle, archive install/uninstall and native platform journeys | Desktop and clients are implemented. MCP shipped in alpha.3; alpha.4 includes desktop/VSIX. Manual GUI coverage on every platform is not established. |
| R06 · Public beta | Onboarding, releases, migrations, exports, operational documentation | Exact-candidate software gates, scale evidence, relevant manual OS checks | Pending; see [release readiness](release-readiness.md). No beta/v1 publication is authorized by this status. |
| R07 · Community platform | Connector catalog, governance, optional operated services | Organic contributions and demonstrated demand | Planned |

## First complete public product

The local + GitHub alpha should include a coherent onboarding journey, Markdown /
text / PDF support, two source types, continuous updates, source filters,
evidence previews, original-location opening on supported clients, and a source
health panel. Ship a useful workflow rather than many disconnected adapters.

## Current delivery priority

As of 19 October 2026, alpha.5 is published with schema 8, paging, passage
windows, freshness guidance, evidence bundles and Sources snapshot transfer.
Its five native targets, packaged browser, installed VSIX, upgrade/restore and
recovery gates passed on the release commit. Public downloads were verified
separately; see the [release record](release-alpha5-verification.md).
Historical alpha.3/schema 6 and alpha.4/schema 7 releases remain immutable.

The owner accepted the supplied Linux candidate, and the final 10k/100k
synthetic measurements are complete. The next priority is voluntary reports
covering installation, actual retrieval and return use, plus relevant manual
Windows and Apple Silicon first-run coverage. Short synthetic measurements do
not establish a stable latency SLO or real-user relevance.
Mid-December 2026 is an internal planning target, not a promised date; index or
migration problems may move it. Search-evaluation work remains in its documented
contribution lane; new connectors and semantic retrieval remain separate decisions.

## Contribution lanes

Document extraction, filesystem edge cases, search evaluation, client
accessibility, connector conformance, distribution, and translations can proceed
as separate community contributions once their contracts are documented.

## Deferred decisions

Email ingestion, OCR, cross-device encrypted sync, and team authorization involve
additional storage, permissions, and packaging work. They remain explicit future
designs. The foundation does not support them.
