# Limited beta feedback and triage

This is the operator handoff for the published `0.1.0-beta.1` limited desktop
beta. It does not create users, publish invitations, or turn downloads into
installation or active-user counts.

## Verified launch identity

The public release is [Findrail 0.1.0-beta.1](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-beta.1),
published from exact `main` revision
`4c0c94e1663804344b1ae1cbcdc3a346208e688a`. Use the release page and its
[SHA256SUMS.txt](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-beta.1/SHA256SUMS.txt)
as the download source. The matching
[release manifest](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-beta.1/release-manifest.json)
reports schema 8, Client API 1 and VSIX 0.1.0.

The frozen release contains five native archives
(`linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, and
`windows-amd64`), the read-only VSIX, `release-manifest.json`, and
`SHA256SUMS.txt`. Do not replace these links with an Actions candidate or a
locally rebuilt archive. The archive's `INSTALL.txt` is the installation
start-here; the repository [beta start guide](beta-start-here.md) is the
privacy and workflow context.

## Audience and first-use path

Invite developers and other local-first users on Windows, Linux and macOS who
have notes, source repositories, technical PDFs or Word documents. Ask for one
coarse outcome and one obstacle, without private documents, queries, paths,
indexes, credentials or raw logs:

1. remember a word;
2. add a folder containing a document they may share only locally;
3. search the remembered word;
4. open the passage and check its source/preview;
5. save a quote and return to it after restarting Findrail.

The product is lexical and local. Do not promise semantic search, OCR, AI,
instant search of arbitrary volumes, or a measured speed improvement. A later
voluntary return report may say whether the collection was still present and
whether search was needed again; first use is not `return_use`.

The functional application is shared across platforms, but evidence is not
transferred between them. Linux amd64 is the primary documented path.
Windows manual coverage remains pending until the planned VM report is
actually received. macOS and Linux arm64 are experimental without matching
human reports. Native CI is not a GUI/OS observation, and the earlier alpha
report is not beta evidence. `final_release=false` remains true.

## Intake and triage

Use the existing optional [desktop first-run form](../.github/ISSUE_TEMPLATE/desktop_first_run.yml)
and [voluntary usage form](../.github/ISSUE_TEMPLATE/voluntary_usage.yml), or
receive a private local JSON report. Do not create a second product intake
system. The offline operator command remains:

```sh
python3 scripts/manual_evidence.py \
  --candidate /abs/evidence/verified-candidate.json \
  --report /abs/evidence/report.json \
  --output /abs/evidence/intake
```

Keep the original report private, check candidate/archive identity and
provenance, and retain only the sanitizer's fixed outcomes in any shared
summary. `intake.json` distinguishes `complete`, `incomplete`, `failed`, and
`mismatch`; a miss (`not_found`) or a not-tried task is never rewritten as a
success. A failed, malformed or mismatched report is visible and blocks that
intake; it is not silently converted to `pending`. Linux first-use output
belongs in `linux-observations.json`, not readiness supplemental evidence.

Prioritize the first reproducible obstacle:

| Signal | Next action |
|---|---|
| Data loss, unsafe action, migration failure, or inability to launch on the declared primary path | Make the blocker visible immediately; preserve the sanitized report. |
| Reproducible crash/hang, missing collection, or stale/freshness error | Prepare a small bug draft with reproduction, expected, actual, and sanitized context. |
| Installation explanation or unclear action | Prepare a bounded UX/documentation fix. |
| New feature or platform request | Record it as a separate idea, not a beta-readiness fix. |

Do not request a private collection or index to reproduce a report. Create a
public issue or send a response only after the owner separately asks; otherwise
keep a reviewable draft outside the product repository.

## Owner handoff

Keep launch copy, invitations, tracker rows, raw reports, synthetic samples,
and completed owner decisions outside this repository. A minimal tracker needs
only: `status` (`invited`, `first_try`, `returned`, `issue`, `closed`),
version, platform, candidate source, anonymous reference, retrieval
`found/not_found`, collection `restored/not_restored`, return use, obstacle,
and severity. Names and email addresses are unnecessary. An empty tracker is
the correct state when nobody has responded.

At each review, report only:

```text
first-use observations: 0
return observations: 0
candidate: 4c0c94e1663804344b1ae1cbcdc3a346208e688a / 0.1.0-beta.1
platform observations: none; Windows pending, macOS and Linux arm64 experimental
obstacles: none received
next fixes: none until a real obstacle is reproduced
```

Replace the zeroes and “none” only with voluntary observations. Do not report
download counts as installations or active users. After the first reports,
rank one to three fixes by integrity/blocking impact, reproducibility, and
frequency; ship each product fix in a separate PR. A later beta.2, stable/v1
release, or change in promised platform support requires a separate release
contract.
