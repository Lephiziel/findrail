# Limited beta owner decision card — blank template

This template is for a future, exact `limited-desktop-beta` decision. The
completed card belongs outside the product repository. Do not commit a filled
card, private human reports, or private references.

This file is intentionally **not** an approval: it records no decision, person,
signature, or consent date. A card or JSON file cannot authenticate its author,
grant identity, or provide repository/write permissions. The owner must make a
separate explicit decision about the named unchanged packet.

## Candidate and evidence identity

| Field | Value |
|---|---|
| Scope | `limited-desktop-beta` |
| Repository | `Lephiziel/findrail` |
| Candidate SHA (exact 40 lowercase hex) | `FILL IN` |
| Candidate version / schema / Client API / VSIX | `0.1.0-beta.1` / `8` / `1` / `0.1.0` |
| Canonical candidate workflow run ID / attempt | `FILL IN` |
| Candidate artifact IDs and GitHub digests (each consumed artifact) | `FILL IN` |
| Performance workflow run ID / attempt | `FILL IN` |
| Performance-series artifact ID / digest | `FILL IN` |
| Performance-soak artifact ID / digest | `FILL IN` |
| Shared extracted Linux amd64 binary SHA-256 | `FILL IN` |
| `release-manifest.json` SHA-256 | `FILL IN` |
| `SHA256SUMS.txt` SHA-256 | `FILL IN` |
| `PACKAGE-MANIFEST.json` SHA-256 | `FILL IN` |
| Completed reports ZIP SHA-256 (outside ZIP) | `FILL IN` |
| Exact release-note body SHA-256 | `FILL IN` |

## Eight application assets

Replace every blank size/hash with the verified byte inventory. These are the
five beta archives plus VSIX, manifest and checksum file; no reports ZIP or kit
is a release asset.

| Basename | Size (bytes) | SHA-256 |
|---|---:|---|
| `findrail_0.1.0-beta.1_linux-amd64.tar.gz` |  |  |
| `findrail_0.1.0-beta.1_linux-arm64.tar.gz` |  |  |
| `findrail_0.1.0-beta.1_darwin-amd64.tar.gz` |  |  |
| `findrail_0.1.0-beta.1_darwin-arm64.tar.gz` |  |  |
| `findrail_0.1.0-beta.1_windows-amd64.zip` |  |  |
| `findrail-readonly-0.1.0.vsix` |  |  |
| `release-manifest.json` |  |  |
| `SHA256SUMS.txt` |  |  |

## Technical outcomes and remaining human categories

For every technical item, record the actual outcome and source artifact/run.
All required technical gates must be passed. A failure, invalid/mismatched
record, missing native gate, recovery, scale, series or soak is not a permitted
limited-scope exception.

| Item | Actual outcome / source |
|---|---|
| Strict software candidate, five native targets, packaged browser and VSIX |  |
| Eight-asset verification and recovery |  |
| Scale 10k / scale 100k |  |
| Performance series / 1800-second soak |  |

List unresolved readiness categories exactly; only the following human
categories may remain pending for an owner decision on limited scope. Keep
unprovided categories pending rather than treating blank kits as reports.

| Category | State and matching report reference, if any |
|---|---|
| `windows_first_run` |  |
| `apple_silicon_first_run` |  |
| `voluntary_usage` |  |

Linux first-run observations are separate informational evidence and are not a
readiness category. Do not transfer Linux amd64 results to Linux arm64.

## Known limitations and exact reviewed notes

| Field | Value |
|---|---|
| Known product/platform limitations | `FILL IN FROM THE VERIFIED HANDOFF` |
| Exact `publication-notes.md` body SHA-256 | `FILL IN` |
| Human categories still pending | `FILL IN FROM readiness-after-scale.json` |
| Handoff evidence-index / reviewed outer ZIP fingerprint | `FILL IN` |
| Card state | `awaiting_owner_decision` |

The card state remains `awaiting_owner_decision` until the owner separately
decides. Do not prefill approval, a decision-maker, a signature, or a consent
date. A later report changes the handoff fingerprint and must be reviewed as a
new exact packet.
