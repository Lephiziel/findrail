# Product specification

## Purpose

Findrail helps people recover information they remember encountering but cannot
locate. It searches their explicitly selected sources and returns useful evidence
with provenance. The long-term product is a personal retrieval platform: one
index, several clients, extensible source adapters, and optional local semantics.

## Product promise

Find the original passage, understand where it came from, and know whether the
indexed copy is fresh. The product must be valuable with ordinary keyword search
and modest hardware before adding optional model-assisted retrieval.

## First audiences

| Audience | Repeated job | Initial sources |
|---|---|---|
| People with extensive notes and documents | Recover a passage without remembering a filename | Folders, Markdown, PDF text |
| Developers and maintainers | Find a decision, implementation note, or past issue | Local repositories, GitHub |
| Self-hosting users and researchers | Own a searchable archive with inspectable data flows | Files, bookmarks, exported documents |

These are hypotheses for user interviews and early usage, not measured market
sizes. Team collaboration and enterprise permission systems come after the
single-user retrieval workflow is dependable.

## Three flagship journeys

1. Choose two folders, index them, search a remembered phrase, inspect the
   matching passage, and copy or open the original location.
2. Connect GitHub read-only, search local notes and issues together, filter by
   repository, and see the time each result was last synchronized.
3. Ask an external AI tool for relevant sources through a read-only interface.
   Findrail returns passages and provenance; the model provider is selected by
   the user and receives only the permitted retrieval output.

The local-folder journey is implemented in the alpha, including PDF pages,
preview and automatic refresh. Location copying is available; a desktop opener
and the connected / AI journeys remain planned.

## Product principles

- Sources are explicit; installation never silently scans the whole disk.
- Results lead with evidence and original location, not generated confidence.
- The index belongs to the user. Export and logical removal are first-class workflows.
- Source content is never executed. Source connectors never edit the origin.
- Search works with a disconnected network for already indexed local data.
- Model services are optional. Choosing one must explain the data it receives.
- Visibility of sync failures and freshness matters as much as ingestion speed.
- Plugins and APIs should create a contribution ecosystem without weakening source scopes.

## Search experience

The completed UI includes a launcher, a search field, source and server-side format/path/title filters,
keyboard navigation, evidence previews, freshness markers, and a source health
page. Results show title, source, matched passage, original location, and indexing
time. Similarity scores must not be described as factual certainty.

Literal search remains the default. Source builds also offer opt-in exact token
phrases, OR, exclusions and token-prefix search; phrases follow the bundled
tokenizer and are not byte substrings. Later hybrid retrieval combines keyword candidates
and semantic candidates, with explicit rank explanations and reproducible
evaluation. Language-specific morphology and typo tolerance remain future work.

## Capability map

| Area | Product direction |
|---|---|
| Sources | Folders; GitHub; bookmarks; Notion; drives; opt-in email imports |
| Documents | Text; Markdown; source code; PDF text; DOCX; optional OCR |
| Sync | Watchers; job queue; resumable cursors; retries; tombstones |
| Retrieval | Keywords; filters; passages; optional embeddings; hybrid evaluation |
| Access | CLI; browser; desktop launcher; editor integration; HTTP; MCP |
| Ownership | Inspect sources; revoke connection; logical remove; portable exports |
| Extension | Versioned connector manifests; conformance suite; sample adapters |

## Scope boundaries

Findrail is a retrieval product. Initial releases do not edit documents, send
messages, schedule user actions, replace a document editor, or attempt to become
a general autonomous assistant. It can supply reliable context to those tools
through explicit, read-only integration surfaces.

## What would make the product worth adopting

The user can install it without provisioning an external database, find useful
passages across at least two sources, understand what was indexed, and recover
from a sync failure. The benchmark is the user's actual information-finding task,
not the number of connectors listed on a landing page.

## Validation targets

For the first public alpha, recruit 10–15 independent testers using their own
data. Track installation completion, successful search tasks, repeated weekly
use, false positive / missed result reports, and requests for additional sources.
Participation and any telemetry are opt-in; the current runtime has no analytics.

Performance targets, to be measured on declared hardware: p95 keyword latency
under 150 ms over a 100k-document test corpus, a documented index size, responsive
cancellation, and bounded extraction memory. These are engineering targets, not
measured properties of the foundation.
