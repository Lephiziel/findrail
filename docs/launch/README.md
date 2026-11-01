# English launch

The first audience hypothesis is developers who keep local Markdown notes,
source repositories and technical PDFs. The first useful promise is finding a
remembered word, inspecting the match and recovering its original location.

## Ready to use

- [Release archives](https://github.com/Lephiziel/findrail/releases): five OS / architecture combinations.
- [Three-document demo](../demo.md): notes, Go code, PDF page 2 and live refresh.
- [Older 37-second demo](../assets/demo.mp4) and [GIF](../assets/demo.gif): a
  narrower alpha.2 workflow; they do not demonstrate the new source manager,
  portable snapshots, collections or evaluation.
- [Short English launch copy](announcement.md): X / Bluesky post and follow-ups.
- [Contributing](../../CONTRIBUTING.md): small tasks and contribution guidance.
- [Project facts and media](press-kit.md): descriptions, version boundaries,
  download links and a reproducible editor walkthrough.
- [Alpha feedback](https://github.com/Lephiziel/findrail/issues/3),
  [Apple Silicon installation](https://github.com/Lephiziel/findrail/issues/4)
  and [multilingual search examples](https://github.com/Lephiziel/findrail/issues/5).

## Initial distribution experiment

The following steps describe the first experiment. The author reports that
the publication step has already happened; use the record below rather than
treating it as a new posting assignment.

1. Publish one visual launch post from the author's existing X account. Use
   the running-app demo, describe shipped behaviour and link to the repository.
   Bluesky or LinkedIn can be the first channel if that is where the author
   already has relevant connections. A new empty account is not a distribution
   advantage by itself.
2. Invite voluntary installation and retrieval feedback. Keep public reports
   free of private documents. Explain limitations and answer product questions.
3. After 48–72 hours, record reach if the platform provides it, repository
   traffic available to the owner, voluntary successful installs and concrete
   obstacles. These counts are different measures; do not call archive
   downloads active users.
4. Fix the most common obstacle. The next post should demonstrate the fix or a
   useful workflow. Expand to another channel when the first experiment gives
   evidence about the audience or message.

The author has already published on X, Reddit and Product Hunt. The small
observed reach does not establish a lack of demand. The next experiment is
three editorial submissions: [Console](https://console.dev/selection-criteria),
[Terminal Trove](https://terminaltrove.com/submit/) and
[Changelog](https://changelog.com/news/submit). These channels accept project
suggestions; inclusion is an editorial decision, not a promised outcome.
Terminal Trove covers CLI tools, so describe the CLI accurately and show its
optional local web interface without calling it a TUI. Do not submit the same
tool by both email and form.

No paid promotion is required for the next editorial experiment. Its purpose
is to reach relevant readers and observe actual use; it does not predict
publication, reach or a number of stars.

## Launch record

| Item | Status |
|---|---|
| GitHub description and topics | Set on 2026-10-02; [setup screenshot](../assets/github-launch-setup.jpg) |
| Demo and English material | In this repository; web recording added on 2026-10-02 |
| Feedback and first contribution tasks | Issues #3, #4 and #5 opened |
| Initial external publication | Reported by the author: [X](https://x.com/lephiziel0/status/2105971042418057666), [r/golang](https://www.reddit.com/r/golang/comments/1wsniww/comment/pdfknf9/?context=3), [r/selfhosted](https://www.reddit.com/r/selfhosted/comments/1wvclw4/comment/pdfiq1o/?context=3), [Product Hunt](https://www.producthunt.com/products/findrail?launch=findrail) |
| Editorial submissions | Prepared separately; sending and acceptance unverified |
| User installations / repeat use | Not measured yet |

Some linked public pages could not be independently retrieved during the
2026-10-07 audit. The publication record follows the author's reports and does
not assert continued public visibility or platform moderation status. Release
asset download counts are not unique installations or active users.

## Version boundaries

The latest published version is [0.1.0-alpha.3](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3). The standalone
archives include DOCX body text, browser folder management, Advanced lexical
queries and filters, offline diagnostics, MCP and public GitHub file snapshots,
portable indexed-text exports/imports, committed indexing reports, root-only
ignore rules, in-tab evidence collections with Markdown export and Literal
result-path evaluation. The connector kit remains experimental.

The [separate release VSIX](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail-readonly-0.1.0.vsix) requires a running compatible API-v1
Findrail server. It is an installable local client, not a Marketplace/Open VSX
listing. The previous alpha.2 archives do not include these additions. The dated
[12 October update](update-2026-10-12.md) remains a historical preparation record;
use the current [facts sheet](press-kit.md#version-boundaries) and
[release verification](../release-alpha3-verification.md) for shipped scope.

The first-user path is archive → checksum → extract → empty start → Add folder →
search → Preview/PDF page → two exact passages with a note → Markdown → restart.
It does not require MCP, GitHub, VS Code or a source build. Collection state lasts
only for the open tab. MCP clients may forward evidence to their model providers.
The owner reported a successful local Word/repository smoke; final automated
Chromium and installed-VSIX checks are linked in the release report. Manual
Finder/Gatekeeper and Windows GUI checks were not inferred from native CI.
Ask volunteers for one short smoke run without private content, paths or logs.
See the [smoke sheet](../release-smoke-alpha3.md).

## Platform constraints checked on 2026-10-02

[X authenticity rules](https://help.x.com/en/rules-and-policies/authenticity)
prohibit spam and inauthentic engagement. A launch does not require fabricated
accounts, purchased stars, repetitive replies or unsolicited bulk messages.

[Hacker News guidelines](https://news.ycombinator.com/newsguidelines.html)
prohibit generated text and automated posting. Any HN submission needs the
human author to write and submit it themselves and participate in discussion.

[DEV's AI article guidance](https://dev.to/guidelines-for-ai-assisted-articles-on-dev/)
restricts promotional AI-assisted articles. The launch copy here is not a DEV
article. A future human-authored technical article should add substantial
information and follow the current [content policy](https://dev.to/terms).
