# Portable evidence bundles (source format)

The source tree defines a strict, portable JSON v1 codec for moving selected
citations and notes. This is distinct from portable source snapshot ZIP v1, which
transfers indexed sources. The codec itself has no storage, HTTP, file access, or
network behavior. Source-cycle management endpoints export saved collections,
preview explicit source mappings, and atomically create new collections after
matching current indexed data. These capabilities are not part of published
alpha.4.

## JSON contract

The UTF-8 payload is exactly one object with exactly these required fields:

```json
{"format":"findrail-evidence-bundle","version":1,"title":"Research","sources":[{"alias":"s1","kind":"filesystem","display_name":"Notes"}],"entries":[{"source_alias":"s1","relative_path":"topic/note.md","media_type":"text/markdown","content_hash":"0000000000000000000000000000000000000000000000000000000000000000","page":0,"start_char":0,"text":"selected excerpt","note":"why it matters"}]}
```

No unknown, missing, or duplicate fields are accepted at any depth. Sources have
exactly `alias`, `kind`, and `display_name`. Aliases are unique `s1` through
`s40`, and every source must be referenced by an entry. Kinds are exactly
`filesystem`, `github`, or `archive`. Display name is only a user hint for
explicit remapping; it does not authorize choosing a source or destination.

Field names are case-sensitive, and required values cannot be `null`. On the
wire `format` and `version` must match exactly; empty format and version zero
are not defaults. Unpaired escaped UTF-16 surrogates are rejected rather than
silently replaced. Source aliases must be unique even before any entries use
them. Canonical empty sources/entries are JSON arrays, never `null`.

Entries have exactly `source_alias`, `relative_path`, `media_type`,
`content_hash`, `page`, `start_char`, `text`, and `note`. Array order is retained;
repeated citations are not coalesced, including when their notes differ.
`content_hash` is the indexed content's 64-character lowercase SHA-256 value; it
does not authenticate an author or source. Page is zero for non-PDF and 1–100000
for PDF. `start_char` is a Unicode code-point offset in the indexed body (or PDF
page), never a byte or UTF-16 offset. No Unicode normalization is performed.
Text contains only the selected excerpt; selecting the whole visible preview
stores that preview as the excerpt. There is no duplicate full-preview field.

Paths are slash-separated relative paths. Absolute paths, drive and UNC paths,
backslashes, NUL, empty components, `.` and `..` components are rejected. Paths
are not rewritten to make a citation appear to match another document. A source
whose real path cannot meet these rules must produce an export error.

The format never contains absolute roots, URIs, destination/source/document/
collection IDs, `original_identity`, or timestamp authority. Destination
matching requires a later explicit operation that verifies current indexed data;
the bundle itself must never select a path or authorize a write.

## Bounds and integrity

| Value | Limit |
|---|---:|
| Raw JSON | 4 MiB |
| JSON nesting depth | 16 |
| Title | nonblank, 256 UTF-8 bytes |
| Sources / entries | 40 / 40 |
| Display name | 512 bytes |
| Relative path | 2,048 bytes and 512 code points |
| Media type | nonblank, 255 UTF-8 bytes |
| Entry metadata (excluding text and note) | 16 KiB |
| Selected text, each / total | nonempty, 256 KiB and 65,536 code points / 2 MiB |
| Note | 2,048 bytes |
| Start offset | 0–2,147,483,647 |

Integers must be JSON integer tokens; fractional and overflowing values are
invalid. Empty source and entry arrays are allowed with a valid title. Readers
reject invalid UTF-8, duplicate keys, unknown fields, trailing values/data and
oversized input before constructing a bundle. Limits are bounded codec-owned
buffers, not a process RSS guarantee. Nothing is silently truncated or evicted.
NUL characters are rejected in names, paths, media types, quotes and notes.

The canonical encoder preserves source/entry order and every string exactly;
its SHA-256 digest detects transfer inconsistency only. It is not a signature,
proof of authorship, or proof that an indexed source is trustworthy.

## Management API

When the writable `start`/desktop composition has a complete collections backend,
`GET /api/v1/collections/{id}/bundle` exports the saved revision as
`findrail-evidence-bundle.json`. `POST /api/v1/collections/import/preview` accepts
`{"bundle": ..., "mappings": {"s1": "local-source-id"}}`; every alias must be
explicitly mapped. The response reports a status for each entry and a short-lived
claim. `POST /api/v1/collections/import` accepts the same bundle, the preview
claim, and a new title. It never merges into an existing collection. Import
rechecks current indexed path, media type, hash, page and quote in one transaction;
any mismatch rejects the entire import. Preview does not read originals or make
network requests.

Bundle requests use the existing management session and origin/host protections.
Wrappers are capped at 5 MiB, raw bundle JSON at 4 MiB, preview output at 256 KiB,
and claims at 32 KiB. Claims expire after five minutes and are process-local;
they do not authenticate bundle authors. `serve` and `demo` do not register these
endpoints. The capability flag is true only for a writable management composition
with a complete bundle backend. These source-cycle capabilities are not part of
the published alpha.4 binary.

Only one HTTP bundle operation runs in the process at a time; `429 bundle_busy`
means retry later. The five-second budget also covers receiving the request body;
`504 bundle_timeout` means it expired. Invalid inputs return 400, exceeded limits
return 413, and an expired preview or changed destination returns 409 and requires
a fresh preview. Unexpected failures return a generic 500 without indexed text,
private paths or database diagnostics. All responses are no-store. An absent
`evidence_bundle` capability on an older API v1 server means false.

Bundles preserve historical selected evidence, not a live link. If an indexed
document has changed, moved, been forgotten, or is otherwise no longer current,
the historical citation cannot automatically be restored to the current
collection. Any future restore workflow must explicitly compare current indexed
data and let the user remap; it must not trust the bundle's display name or hash
as authority.

In the browser, **Export evidence bundle** downloads a saved collection as JSON;
the file contains its selected citations and notes. Save pending edits before
export. To import, choose a JSON file and explicitly map every source alias to a
source in the current inventory. The server preview checks all citations and
quotas before enabling creation of a new collection. Historical missing evidence
cannot be restored automatically; changed hashes, pages, or quotes require a new
preview and never result in a partial import. This describes the source-tree UI;
the workflow is not included in the published alpha.4 binary.
