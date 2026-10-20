# Findrail alpha.5: published release verification

Published at `2026-10-19T15:33:27Z`; public downloads independently verified on 2026-10-20.

- Release: [Findrail v0.1.0-alpha.5](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.5); public prerelease, not a draft.
- Tag: `v0.1.0-alpha.5`; commit `cb53ee9638f69b47b386921e41368f3b66ceddde`.
- Publication gates: [exact release-commit workflow](https://github.com/Lephiziel/findrail/actions/runs/39276940977), all 17 required jobs passed.
- Authorization and release preparation: [PR #90](https://github.com/Lephiziel/findrail/pull/90).
- Application / extension / client API / schema: `0.1.0-alpha.5` / `0.1.0` / `1` / `8`.
- All eight public downloads passed exact inventory, size, SHA-256, manifest, archive BUILD-INFO and VSIX checks.

## Downloaded public assets

| File | Size (bytes) | SHA-256 |
|---|---:|---|
| [SHA256SUMS.txt](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/SHA256SUMS.txt) | 724 | `f587ffc579c133f6f5f204b5bddd88fbc174aaa9341430fb701832dd5d781423` |
| [findrail-readonly-0.1.0.vsix](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/findrail-readonly-0.1.0.vsix) | 18,033 | `46511dfcb158759d32d9f2a30a856f377bfae0031c905ac6d44f4f387b21ad9b` |
| [findrail_0.1.0-alpha.5_darwin-amd64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/findrail_0.1.0-alpha.5_darwin-amd64.tar.gz) | 19,295,280 | `f6599dbcbdb4e704999a134d493cde7d7e6ae10a0a9c129ed9a2834d38e79f95` |
| [findrail_0.1.0-alpha.5_darwin-arm64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/findrail_0.1.0-alpha.5_darwin-arm64.tar.gz) | 18,193,135 | `b12c5d199e9d09948ce1a1a112ec1e43314b91af4f44995b6a13158e0374e717` |
| [findrail_0.1.0-alpha.5_linux-amd64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/findrail_0.1.0-alpha.5_linux-amd64.tar.gz) | 12,846,015 | `78885728107e26be6db18f93c0f94ee6af92b585f00aea21dd93ba9e19a5a8f0` |
| [findrail_0.1.0-alpha.5_linux-arm64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/findrail_0.1.0-alpha.5_linux-arm64.tar.gz) | 11,860,456 | `23727c7b6d68871148347717e9c0e19998f079f475af16e32846db8f233b2905` |
| [findrail_0.1.0-alpha.5_windows-amd64.zip](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/findrail_0.1.0-alpha.5_windows-amd64.zip) | 31,845,128 | `0020f7e374c8993f31cf4f7d9688310d42fe50b3905459d1705fa3f41bbe7014` |
| [release-manifest.json](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/release-manifest.json) | 1,616 | `28ed088d474117f9ce0614626b7d62b47ed27e6646e5670d52affeeeee9031b7` |

These hashes were computed from the actual public download bytes and matched
GitHub's asset digests. `SHA256SUMS.txt` covers the five archives, VSIX and
manifest; its own digest is recorded above. Every archive has clean BUILD-INFO
matching the resolved tag, target, alpha.5 version and schema 8. All five archives
passed safe extraction and launcher/install-resource checks. The VSIX retains
extension identity `findrail-local.findrail-readonly`, version 0.1.0, for API 1.
Checksums detect consistency and are not a publisher signature.

## Release gates

All jobs below completed successfully in the same canonical-main workflow run
on the actual release commit. PR-head artifacts were not substituted for the
release-commit packages.

| Job | Result |
|---|---|
| [validate-source](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143622773) | Passed |
| [native-packages (linux-amd64, ubuntu-latest)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689499) | Passed |
| [native-packages (darwin-arm64, macos-14)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689551) | Passed |
| [native-packages (linux-arm64, ubuntu-24.04-arm)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689558) | Passed |
| [native-packages (darwin-amd64, macos-15-intel)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689652) | Passed |
| [native-packages (windows-amd64, windows-latest)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689830) | Passed |
| [source-checks / race](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689845) | Passed |
| [source-checks / ui](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143689930) | Passed |
| [source-checks / check (ubuntu-latest)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143690030) | Passed |
| [source-checks / check (windows-latest)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143690106) | Passed |
| [source-checks / check (macos-latest)](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118143690120) | Passed |
| [packaged-browser-journey](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118145637322) | Passed |
| [installed-vsix](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118145637375) | Passed |
| [aggregate-and-verify](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118147479229) | Passed |
| [reusable-final-verification / verify-assets](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118147872458) | Passed |
| [release-state](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118149330623) | Passed |
| [publish](https://github.com/Lephiziel/findrail/actions/runs/39276940977/job/118149387608) | Passed |

The retained strict verification summary was downloaded and revalidated with
`scripts/readiness.py` against the exact release SHA/version, fixed native and
browser case inventories, schema/API versions and related binary/archive hashes.
Five native targets executed on their target OS/architecture. Browser automation
used the extracted Linux package and synthetic fixtures; installed VSIX, generated
MCP configuration, upgrade/restore, integrity and recovery checks remain distinct
parts of the release evidence.

## Independent Linux public-download check

The Linux amd64 archive was safely extracted outside the checkout. Its actual
`findrail version` output and BUILD-INFO matched alpha.5, schema 8 and the tag.
The executable digest matched the binary that passed the release browser journey:

`6fc6fe384171a39d3c2809bdf8e923a089cee21516b04a5d3dfb419e0e011a96`

This local check executed the public CLI version command. The full browser and
native lifecycle coverage comes from the matching release CI, not a newly claimed
manual desktop test or a locally rebuilt application.

## Owner acceptance, scale and coverage

On 19 October 2026 the owner reported the supplied Linux acceptance procedure
worked: desktop start, synthetic Markdown/DOCX/PDF retrieval, document refresh,
and saved collection/note restoration after Quit. The manually supplied candidate
was `109678f69e76c2a0b64a64ef8566c41e570e1a13`; its exact archive and bounded
[acceptance scope](platform-installation.md#linux-amd64-alpha5-source-candidate-2026-10-19)
remain recorded separately. The release merge changes version/release documentation;
its product code objects match that accepted base. Distro, browser and refresh
method were not separately reported. This is not manual Windows or Apple Silicon
first-run acceptance.

The [pre-release resource record](resource-baseline.md) retains the 109678f run
whose 100k warm HTTP p95 was 201.291 ms. A later run on release-PR head
`6339c6fafcc8a09888f5b8a2266d2be362fdf094` recorded 146.797 ms, with ten
measured warm requests per run. Product code was unchanged; this difference
does not demonstrate an optimization or a stable latency SLO. See the
[release PR](https://github.com/Lephiziel/findrail/pull/90) for the later run.
Independent real-user retrieval and return-use evidence remains open.

## Distribution and upgrade boundaries

Alpha.5 ships passage windows, paged results and presets, explicit evidence
bundles, Sources snapshot transfer, freshness guidance and recovery checks.
Desktop launch, optional original opening, saved named collections and read-only
MCP/VSIX remain available. `serve`/`demo` retain read-only management boundaries
and temporary collections. Portable source ZIPs do not contain saved collections;
evidence bundles transfer selected citations/notes after explicit source mapping.

Alpha.5 uses schema 8; alpha.4 uses schema 7 and alpha.3 uses schema 6. Stop all
Findrail processes and back up the whole data directory, including WAL/SHM files
when present, before upgrading. Alpha.4 cannot open an upgraded schema-8 index.
Rollback requires the complete stopped pre-upgrade backup and matching old binary.
[Installation and rollback](../build/package/INSTALL.txt).

Packages remain unsigned and unnotarized; macOS requires 13+. Native adapters
and automated browser checks do not certify Finder/SmartScreen, every external
viewer, browser or assistive technology. OCR, semantic search, private GitHub,
team authentication and automatic updates are not included.

README screenshots remain identified as alpha.3 captures. The historical
[alpha.3](release-alpha3-verification.md), [alpha.4](release-alpha4-verification.md)
and 18 October source-cycle records retain their original scope. Updating current
documentation does not change the published tag, source revision or asset bytes.
