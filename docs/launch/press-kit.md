# Findrail: project facts and media

Checked on 2026-10-13. Findrail is an Apache-2.0 open-source project built in Go.
The current published local alpha is [0.1.0-alpha.3](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3). Five native
archives, a separate VSIX, a release manifest and shared checksums were downloaded
and verified against the release commit. See [release evidence](../release-alpha3-verification.md).

## Short description

Local keyword search for folders of notes, code and text PDFs, with matching
passages, PDF page numbers and original file locations.

## Editor description

Findrail is an open-source Go app for developers who keep notes, code and
technical PDFs in different folders. Select a folder, search words you remember,
preview the indexed text and copy the original file location. Text PDF results
preserve their page numbers. Local folders refresh while the app runs. The
released alpha includes standalone archives for Linux, macOS and Windows, a
CLI and an optional loopback web interface. Keyword search requires no model,
account, document upload or external database.

## Try the released workflow

1. Download the matching archive and [checksums](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/SHA256SUMS.txt) from
   [alpha.3](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3), verify the hash, and extract it.
2. Run `./findrail start` on Linux/macOS or `.\findrail.exe start` on Windows.
   Open the printed loopback URL if the browser does not appear. A new index
   starts empty; existing indexes resume registered sources.
3. Choose **Add folder**, then search a remembered term in a technical document.
4. Open **Preview**, inspect the indexed passage and PDF page when applicable.
   Add two exact passages and a note to the collection, then download Markdown.
5. Stop with Ctrl+C and restart using the same data directory. The index persists;
   the in-tab collection clears on reload. Keep the downloaded Markdown separately.

Go, Python and an external database are not needed at runtime. Binaries are
unsigned; macOS may require an app-specific allowance in Privacy & Security.
Back up a stopped data directory before upgrading schema 2 to 6; see
[install/upgrade](../../build/package/INSTALL.txt). The optional
[VSIX](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail-readonly-0.1.0.vsix) needs a separate API-v1 server. The
[demo guide](../demo.md) describes the older synthetic three-document workflow.
See [release notes](../release-notes-alpha3.md) and the
[manual smoke sheet](../release-smoke-alpha3.md).

## Version boundaries

| Capability | Published alpha.2 archives | Published alpha.3 archives | Separate distribution |
|---|---|---|---|
| Local text, Markdown and common code files | Included | Included | — |
| Text PDFs, page attribution, snapshot preview, Copy location | Included | Included | — |
| Folder refresh, `start`, three-document `demo` | Included | Included | — |
| Copy preview text as Markdown with its source | Not included | Included | — |
| Scoped read-only stdio MCP | Not included | Included; [configuration](../mcp.md) | — |
| Commit-pinned public GitHub file snapshots | Not included | Included; [instructions](../github.md) | — |
| Empty-index onboarding and web source management | Not included | Included; [instructions](../source-management.md) | — |
| Bounded DOCX body-text indexing; legacy sources disabled by default | Not included | Included; [supported subset](../docx.md) | — |
| Advanced lexical queries and format/path/title filters | Not included | Included; [query semantics](../search-query.md) | — |
| Read-only offline `doctor` | Not included | Included | — |
| Portable snapshots | Not included | Indexed plaintext and provenance, not original files; import is frozen, not a frozen database; [scope](../portable-snapshots.md) | — |
| Indexing report | Not included | Last committed successful scan only, not failed-attempt history; [details](../indexing-diagnostics.md) | — |
| `.findrailignore` | Not included | Root-only rules; failed policy scan retains prior committed content/report; [syntax](../folder-ignore.md) | — |
| Evidence collection | Not included | In-tab only; reload clears it; explicit Markdown download; [scope](../evidence-collections.md) | — |
| `evaluate` | Not included | Read-only Literal path sets, 1–100 cases, at most 100 results/case; incomplete is error; mismatch nonzero after full report; [details](../search-evaluation.md) | — |
| VS Code read-only client | Not included | Requires compatible server/client API v1; [setup](../vscode.md) | [Separate release VSIX](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail-readonly-0.1.0.vsix); not on Marketplace/Open VSX |
| Connector kit | Not included | Experimental; independent adoption not established; [guide](../connectors.md) | — |
| Marketplace / Open VSX listings | Not published | Not published | Not published |
| OCR, semantic retrieval, private GitHub | Not included | Not implemented | — |

Search is literal keyword retrieval, not an AI answer or a promise of semantic
recall. The app searches explicitly indexed sources and previews saved text.
The current web interface is loopback only. GitHub refresh is manual; repository
issues, PR discussions, remote PDFs and private repositories are not indexed.
An MCP client may forward returned evidence to its own AI provider. Local
indexing does not establish another client's privacy policy.

## Media

- [37-second original web recording](../assets/demo.mp4)
- [Animated preview](../assets/demo.gif)
- [PNG from the recording](../assets/demo.png)
- [Three-document demo instructions](../demo.md)

The recording uses synthetic documents and shows the narrower old alpha.2 demo
workflow. It is not a performance benchmark, a customer testimonial, or a demo
of the new source management, portable imports, collections or evaluation. It is
not evidence for candidate/release UI checks; use the manual smoke sheet for that.

## Links and feedback

- [Source](https://github.com/Lephiziel/findrail)
- [Alpha.3 release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3)
- [Previous alpha.2 release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.2)
- [Alpha feedback](https://github.com/Lephiziel/findrail/issues/3)
- [Architecture](../architecture.md)
- [License](../../LICENSE)

A useful first report is the OS, version, whether the app started, whether a
remembered word led to the right original document, and one obstacle. Public
feedback should use synthetic or redacted examples. Archive downloads and
repository stars are not counts of independent active users.
