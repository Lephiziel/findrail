# Alpha.3 submission kit

**Prepared 2026-10-13.** The published release and its public assets were
verified; this kit is ready to use for the release as it exists today. It does
not mean any submission was made. Findrail remains **0.1.0-alpha.3**, a local
alpha / pre-1.0 release, not stable software. Budget: $0. Do not buy Terminal
Trove Express Lane or other placement.

## Verified routes and rules (2026-10-13)

- [Console selection criteria](https://console.dev/selection-criteria): accepts
  tool details at **hello@console.dev**. Its criteria specifically cover useful
  developer tools, individual self-service trial, quality, documentation and
  pre-1.0 alpha/beta labeling. [Console](https://console.dev/) describes its
  weekly newsletter; no feature or reach is promised.
- [Terminal Trove submit](https://terminaltrove.com/submit): a form at no stated
  charge, or an email to **curator@terminaltrove.com**. Its criteria ask for
  cross-platform availability where possible, prefer standalone binaries,
  require a PNG/GIF/MP4 preview, and say the tool must not already be listed.
  Check the [current catalog](https://terminaltrove.com/list/) immediately
  before submitting. The form asks for project details, a preview, install
  instructions, author confirmation and email. The optional Express Lane is
  paid ($199) and is explicitly out of scope. Choose **one route only**; do not
  submit both email and form for the same listing.
- [Changelog News submit](https://changelog.com/news/submit): requires sign-in
  or sign-up; the form has URL, Title and “What's interesting about it?” fields.
  Its stated policy welcomes self-submissions, excludes commercial products,
  and asks submitters to explain why the item is newsworthy. Submission is free
  on the public page; no publication is promised.

## Release links and fact boundary

- Project: <https://github.com/Lephiziel/findrail>
- Actual alpha.3 release: <https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3>
- Actual checksum: <https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/SHA256SUMS.txt>
- Current source/release facts: [press kit](press-kit.md),
  [published-release verification](../release-alpha3-verification.md),
  [release notes](../release-notes-alpha3.md),
  [installation/upgrade instructions](../../build/package/INSTALL.txt).
- Scope references: [evidence collections](../evidence-collections.md),
  [portable snapshots](../portable-snapshots.md),
  [search and filters](../search-query.md),
  [DOCX support](../docx.md), [indexing diagnostics](../indexing-diagnostics.md),
  [evaluation](../search-evaluation.md), and [MCP](../mcp.md).
- Existing visual preview: [PNG](../assets/demo.png) ·
  [GIF](../assets/demo.gif) · [37-second MP4](../assets/demo.mp4).
  These are the older alpha.2 demo: keyword search, PDF page 2 and live note
  refresh only. They do **not** demonstrate alpha.3 onboarding, DOCX,
  collections, portable snapshots or evaluation. Say so when linking them;
  do not represent them as an alpha.3 feature demo.

Alpha.3 is a downloadable, unsigned prerelease. The five standalone archives,
separate VSIX, manifest and shared checksums were downloaded and verified at the
release commit as recorded in the verification document. Go, Python, a model
account and document upload are not required at runtime. It is Apache-2.0.

### Claims to preserve

- The browser evidence collection exists in the open tab only; reload/close
  clears it. Markdown download is explicit and may contain private text and
  local paths. Collections are bounded, not a persistent notebook.
- Preview and export use bounded **indexed plaintext snapshots**, not a live
  read of originals. Portable snapshots carry indexed text and provenance,
  not original documents or a frozen database; imports are independent frozen
  sources. DOCX text has no page-number claim.
- OCR and semantic retrieval are absent. Text-only PDF support does not include
  scanned-PDF OCR. Public GitHub file refresh is manual; private GitHub support
  is absent. MCP is scoped/read-only, but an MCP client may apply its own
  provider policy and forward returned text.
- Source reports represent the last committed successful scan, not failed-run
  history. `evaluate` checks Literal result path sets for 1–100 cases (up to
  100 results per case), is not a ranking/relevance benchmark, treats
  incomplete cases as errors, and reports all cases before returning nonzero
  for mismatch.
- Connector conformance remains experimental; independent adoption is not
  established. The VSIX is a separate release asset, requires a compatible
  running API-v1 server, and is not on Marketplace/Open VSX. Do not claim
  private GitHub, beta/stable status, code-signing, Marketplace availability,
  or user counts.

## 1. Console email — ready to send

**To:** hello@console.dev

**Subject:** Findrail alpha.3 — local search for technical notes, code and PDFs

```text
Hi Console team,

Findrail helps developers recover a remembered phrase from their own technical notes, code and documents. It is a self-service, open-source Go application: download a standalone archive, verify its checksum, extract it and start searching; Go is not needed at runtime.

The current 0.1.0-alpha.3 release starts with an empty local index and lets a developer add a folder, inspect the indexed passage and source (including a text-PDF page), then collect two exact passages with a note and explicitly download them as Markdown. It also indexes bounded DOCX body text, without page numbers. The collection stays in the browser tab and clears on reload.

Project: https://github.com/Lephiziel/findrail
Release and verified downloads: https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3
Project facts and alpha.2-vs-alpha.3 scope: https://github.com/Lephiziel/findrail/blob/main/docs/launch/press-kit.md
Release verification: https://github.com/Lephiziel/findrail/blob/main/docs/release-alpha3-verification.md
Install/upgrade notes: https://github.com/Lephiziel/findrail/blob/main/build/package/INSTALL.txt

This is an alpha, pre-1.0 release for evaluation, not a stable product. Search is literal/lexical rather than semantic; OCR and private GitHub sources are not included. The archives are unsigned. No model account or document upload is required for the local runtime.

Thank you for considering it,
Findrail maintainer
```

## 2. Terminal Trove — choose one submission route

Check the catalog first. The catalog page inspected on 2026-10-13 did not show
Findrail, but availability can change. Ensure the linked project is still not
listed before filing. The **form is recommended** because it matches the
published structured fields and preview requirement; fill it once and do not
also email the curator. If the form is unavailable, use the alternative email
below instead, never both.

### Form values (one form submission)

- **name:** Findrail
- **url:** `github.com/Lephiziel/findrail`
- **tagline (~100 chars):** Local-first search for developer notes, code, PDFs and DOCX
- **source code:** `https://github.com/Lephiziel/findrail`
- **Description (100–400 chars):** Findrail is an open-source Go app for searching selected folders of technical notes, code, text PDFs and DOCX locally. Download a standalone archive, add a folder in the optional loopback browser UI, inspect indexed passages and their source, and export selected citations as Markdown. Alpha.3 is pre-1.0.
- **2–3 standout features (60–400 chars):** Standalone Linux, macOS and Windows archives with local SQLite; Literal search by default and opt-in Advanced queries with format/path/title filters; bounded source reports and a read-only `evaluate` command for Literal path-set checks. Optional loopback browser UI; Findrail is a CLI, not a TUI.
- **What makes it different? (50–500 chars):** Search runs locally against an explicit index, with inspectable indexed-text snapshots and source provenance. An optional browser UI supports in-tab evidence collections and explicit Markdown export; the collection clears on reload. Portable snapshots preserve indexed text and provenance, not original files or a database backup.
- **Who is this for / when to use it? (60–300 chars):** Developers who keep technical notes, code and text documents across local folders and want to find a remembered phrase, inspect the matching source, and save a few citations without uploading documents or configuring a model account.
- **Anything else (optional; not published):** Version 0.1.0-alpha.3 is a prerelease, not stable. DOCX results have no page numbers. OCR and semantic retrieval are absent. Archives are unsigned. Existing GIF/MP4 show the narrower alpha.2 demo, not these new features. Please use the release notes and scope links below when reviewing.
- **Primary language:** Go
- **License:** Apache-2.0
- **Project type:** Open source
- **Preview image (PNG):** `https://github.com/Lephiziel/findrail/blob/main/docs/assets/demo.png` — older alpha.2 UI, not an alpha.3-feature demo. The form requires an image preview; use this only with that honest label. Do not claim a new alpha.3 visual has been recorded.
- **GIF (optional):** `https://github.com/Lephiziel/findrail/blob/main/docs/assets/demo.gif` — older alpha.2 search/PDF/live-refresh flow only.
- **Categories:** Development; Files & Data Management; Terminal & Shell (only if the form permits this CLI; it is not a TUI).
- **Install instructions:**

  ```text
  Linux/macOS: Download the matching archive and SHA256SUMS.txt from https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3, verify its checksum, extract it, then run ./findrail start. Open the printed loopback URL and choose Add folder.
  Windows PowerShell: Download the Windows archive and SHA256SUMS.txt from https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3, verify its checksum, extract it, then run .\findrail.exe start. Open the printed loopback URL and choose Add folder.
  No Go or external database is required at runtime. Archives are unsigned; see bundled INSTALL.txt for platform and upgrade cautions.
  ```
- **Author:** Yes
- **Email:** enter the owner's own submission contact in the form; do not use an agent address.
- Confirm criteria only after checking the catalog and preview requirement.

Useful references to include in the optional information or listing description:
[release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3),
[press kit](https://github.com/Lephiziel/findrail/blob/main/docs/launch/press-kit.md),
[installation](https://github.com/Lephiziel/findrail/blob/main/build/package/INSTALL.txt),
[release notes](https://github.com/Lephiziel/findrail/blob/main/docs/release-notes-alpha3.md),
[portable snapshot scope](https://github.com/Lephiziel/findrail/blob/main/docs/portable-snapshots.md),
[search modes](https://github.com/Lephiziel/findrail/blob/main/docs/search-query.md),
[evaluation limits](https://github.com/Lephiziel/findrail/blob/main/docs/search-evaluation.md).

### Alternative email route (use only instead of the form)

**To:** curator@terminaltrove.com

**Subject:** Findrail alpha.3 — local-first Go CLI for technical documents

```text
Hi Terminal Trove curator,

Would you consider listing Findrail, an open-source Go CLI for local-first search across selected folders of notes, code, text PDFs and DOCX? It uses a local SQLite index and ships as standalone Linux, macOS and Windows archives; Go and an external database are not required at runtime.

Literal search is the default. Opt-in Advanced queries and format/path/title filters are also available. The read-only `evaluate` command compares Literal result path sets (not ranking quality), and source reports show the last committed successful scan. An optional loopback browser UI supports source management, inspectable indexed-text previews, in-tab evidence collections and explicit Markdown export. This is a CLI with an optional browser UI, not a TUI.

Release and installation: https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3
Project: https://github.com/Lephiziel/findrail
Press kit / exact scope: https://github.com/Lephiziel/findrail/blob/main/docs/launch/press-kit.md
Install/upgrade: https://github.com/Lephiziel/findrail/blob/main/build/package/INSTALL.txt
PNG preview (older alpha.2 demo only): https://github.com/Lephiziel/findrail/blob/main/docs/assets/demo.png
GIF preview (older alpha.2 search, PDF page and live-refresh flow only): https://github.com/Lephiziel/findrail/blob/main/docs/assets/demo.gif

Alpha.3 is pre-1.0 and the archives are unsigned. Portable snapshots contain indexed text and provenance, not original files or a frozen database; imported sources are frozen. Collections clear on tab reload. DOCX has no page numbers; OCR and semantic retrieval are absent. Public GitHub refresh is manual, and private GitHub is not supported. The connector kit is experimental and independent adoption is unverified.

I checked the current catalog before sending and did not find Findrail listed. I am using this email route only, not submitting the form as well.

Thanks for taking a look,
Findrail maintainer
```

**Preview caveat:** Terminal Trove currently requires a PNG preview. The linked
PNG is real and available but depicts the narrower alpha.2 workflow. It is
acceptable only if the curator is told that explicitly; it is not a substitute
for a future alpha.3 recording.

## 3. Changelog News form — ready fields

The page requires sign-in/sign-up to submit. The fields below do not bypass
that requirement.

**URL**

```text
https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3
```

**Title**

```text
Findrail alpha.3 adds local DOCX search, evidence collections and portable indexed-text snapshots
```

**What's interesting about it?**

```text
Findrail is an Apache-2.0 Go project whose alpha.3 release turns local search into a complete, self-service workflow: start with an empty index, add a folder, inspect a matched passage and source, collect exact evidence with a note, and explicitly export Markdown. Five standalone OS/architecture archives are published with checksums; Go, Python, a model account and document upload are not required at runtime. New scope includes bounded offline DOCX body text (no page claims), opt-in Advanced lexical queries, portable snapshots of indexed plaintext and provenance (not original files or a database backup), last-successful indexing reports, and read-only Literal path-set evaluation. Collections live only in the browser tab. This is an unsigned pre-1.0 alpha: OCR, semantic retrieval and private GitHub are absent; public GitHub refresh is manual.
```

The claim is a shipped release/result, not a forecast or audience claim. Do not
submit until the duplicate check below is complete.

## Optional X post (only after release; not required)

```text
Findrail alpha.3 is out: search local notes, code, text PDFs and DOCX, inspect indexed evidence, and export selected citations as Markdown. Apache-2.0, pre-1.0; OCR and semantic search aren't included.

https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3
```

## Owner checklist (about 10–15 minutes)

1. Check sent mail, saved browser state/submission confirmations, and any
   available Console, Terminal Trove or Changelog account history. The agent
   cannot inspect the owner's inbox or form account; previous submission status
   is **unknown**, not “not sent”. If history is unavailable, ask the owner
   only whether that same channel/submission was already sent.
2. If a prior submission is found, do not send a duplicate. Record the known
   date, route and response in the log. If unknown, pause that channel until
   checked; do not treat uncertainty as permission to duplicate.
3. Open the project/release links above. Confirm alpha.3 remains the latest
   applicable release, the actual asset links work, and the current docs match
   the proposed copy. Do not silently reuse this kit for a newer release.
4. For Console, open a new email, paste the subject and full message, review
   recipient/links, then send once.
5. For Terminal Trove, re-check [the live catalog](https://terminaltrove.com/list/)
   and [current form/rules](https://terminaltrove.com/submit). Pick either
   form or email, never both. Provide the existing PNG only with the older-demo
   caveat; if it no longer satisfies the preview criteria, stop rather than
   inventing a new asset.
6. For Changelog, sign in on the official page, paste the three fields, inspect
   the final preview, then submit once.
7. After each actual submission, record the date, chosen route and verified
   release link in `alpha3-submission-log-template.md`; set response and next
   action to what actually happened. Do not record drafts as submissions.

## If the first cycle yields no user trials

Prepare only a later, separate experiment of up to five contextual invitations
in relevant discussions that already exist. Immediately before that experiment,
find current discussions and check each community's current sharing rules.
Invite only in context; no mass outreach, random-person targeting, automated
daily reposts or repetitive cross-posting. No invitations are prepared or sent
by this kit.
