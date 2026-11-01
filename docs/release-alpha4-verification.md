# Findrail alpha.4: published release verification

Verified on 2026-10-14 after publication at 2026-10-14T12:39:43Z.

- Release: [Findrail v0.1.0-alpha.4](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.4); public prerelease, not a draft.
- Tag: `v0.1.0-alpha.4`; commit `1d47f9d54ff81ab566fcf3f8c0c6bf9af86a33f7`.
- Publication gates: [exact release-commit workflow](https://github.com/Lephiziel/findrail/actions/runs/38564851132).
- Application / extension / client API / schema: `0.1.0-alpha.4` / `0.1.0` / `1` / `7`.
- All eight public downloads passed exact asset-set, size, SHA-256, manifest, archive BUILD-INFO and VSIX contract checks.

## Downloaded public assets

| File | Size (bytes) | SHA-256 |
|---|---:|---|
| [SHA256SUMS.txt](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/SHA256SUMS.txt) | 724 | `cb9a9efd63f007b9567c3d0858c190e51273c9f5d918ba4e3e9072a6e2563d89` |
| [findrail-readonly-0.1.0.vsix](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/findrail-readonly-0.1.0.vsix) | 18,033 | `b03c14d9f892852ecd3aba200e624d1af2358c6b284d23f9a495297d88315b18` |
| [findrail_0.1.0-alpha.4_darwin-amd64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/findrail_0.1.0-alpha.4_darwin-amd64.tar.gz) | 18,924,001 | `6bd6023b83ffc736f438d1c92c60e0ca8f62f8f0dba15a8c6b9b6f66e5315ac0` |
| [findrail_0.1.0-alpha.4_darwin-arm64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/findrail_0.1.0-alpha.4_darwin-arm64.tar.gz) | 17,835,989 | `68f70451f6c2acf70cd4c1227423f7865948d6aeccbe95d702e82fd6004fc743` |
| [findrail_0.1.0-alpha.4_linux-amd64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/findrail_0.1.0-alpha.4_linux-amd64.tar.gz) | 12,599,144 | `41f91c62b618a64dc083f7a63403f7f4fcc6f8b13ffb613acfee6704927f6a1a` |
| [findrail_0.1.0-alpha.4_linux-arm64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/findrail_0.1.0-alpha.4_linux-arm64.tar.gz) | 11,631,358 | `6c571b9435d39afa0c7b90c971e2d075f169dbddd5d056a766dfc740202b4651` |
| [findrail_0.1.0-alpha.4_windows-amd64.zip](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/findrail_0.1.0-alpha.4_windows-amd64.zip) | 31,114,007 | `ea983b5a2559ffb44a7394efc009cfc067d881f89f623b53b4b65037fd10bd49` |
| [release-manifest.json](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/release-manifest.json) | 1,616 | `87e3671b73c379b0a1da2435cc2f98734bd860b0478341ea515005baec83f153` |

These hashes were computed from the public release downloads and matched
GitHub's asset digests. `SHA256SUMS.txt` covers the five archives, VSIX and
manifest; its own public-download digest is recorded above. Every archive's
clean BUILD-INFO and the manifest source revision match the release tag.
Safe extraction and the required launcher/install resources were checked for
all five archives. Checksums are not a publisher signature.

## Release gates

The following jobs ran against the actual release commit, after
[PR #46](https://github.com/Lephiziel/findrail/pull/46) merged. Publication
succeeded on the first attempt.

| Check | Result / scope |
|---|---|
| [validate-source](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854806032) | Passed at the release commit |
| [native-packages (darwin-amd64, macos-15-intel)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854866857) | Passed at the release commit |
| [native-packages (linux-arm64, ubuntu-24.04-arm)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854866930) | Passed at the release commit |
| [native-packages (darwin-arm64, macos-14)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854867188) | Passed at the release commit |
| [native-packages (linux-amd64, ubuntu-latest)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854867275) | Passed at the release commit |
| [native-packages (windows-amd64, windows-latest)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854867340) | Passed at the release commit |
| [source-checks / check (macos-latest)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854867794) | Passed at the release commit |
| [source-checks / race](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854867884) | Passed at the release commit |
| [source-checks / check (ubuntu-latest)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854867966) | Passed at the release commit |
| [source-checks / ui](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854868050) | Passed at the release commit |
| [source-checks / check (windows-latest)](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115854868144) | Passed at the release commit |
| [installed-vsix](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115856010021) | Passed at the release commit |
| [packaged-browser-journey](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115856010347) | Passed at the release commit |
| [aggregate-and-verify](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115856558487) | Passed at the release commit |
| [reusable-final-verification / verify-assets](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115856749331) | Passed at the release commit |
| [release-state](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115856865254) | Passed at the release commit |
| [publish](https://github.com/Lephiziel/findrail/actions/runs/38564851132/job/115856918179) | Passed at the release commit |

The native package jobs execute on their target OS/architecture; they are
separate from cross-compilation. Their upgrade, backup/restore, launcher and
installed-package checks do not imply certification of every GUI or viewer.

## Independent public-download checks

The Linux amd64 archive was downloaded from its public release URL and safely
extracted outside the checkout. Its CLI and desktop launcher were exercised
with fresh temporary indexes and fictional files.

| Check | Result / scope |
|---|---|
| Version and BUILD-INFO | `0.1.0-alpha.4`, schema 7, clean source revision matching the tag |
| CLI / HTTP smoke | Passed: empty start, source management APIs/jobs, demo read-only boundary, search, PDF and DOCX extraction, previews, rename refresh and failed-scan rollback |
| Desktop lifecycle | Passed: first and second launch reuse one verified owner; protected Quit drains and removes its instance record |
| Desktop browser opener | Controlled executable adapter, not a real desktop browser/viewer test |
| Foreign-platform public archives | Bytes, safe extraction, resources and build metadata verified; their executables were run in native release CI, not on the local Linux verifier |
| Historical alpha.3 | Tag still points to `d5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb`; all eight asset IDs, names, sizes, SHA-256 digests and update timestamps match the pre-release baseline |

## Owner acceptance and coverage

On 2026-10-14 the owner reported that everything in the supplied short check
worked. The [acceptance record](release-smoke-alpha4.md) covers menu/first launch,
repeat launch and tab reuse, Quit, originals/folders, restoring a saved
collection, and uninstall preserving data. The supplied Linux amd64 candidate
was `a0d44f53015287c50881bfd65be93d5f6aaf22cf`; exact OS version, CPU,
viewer and security-prompt details were not separately reported.

The acceptance report predates the documentation-only acceptance commit and
final main release merge. Full release gates rebuilt and checked the final
release SHA; no candidate archive was renamed into a public release. The
owner's overall report is not a per-step test log or manual GUI coverage of
all platforms. macOS and Windows GUI acceptance are not independently recorded.

## Distribution and upgrade boundaries

Alpha.4 includes the separate desktop launcher and per-user menu/app/shortcut
resources, opt-in local original/folder opening, and explicitly saved named
collections in `start` / desktop. `serve` / `demo` collections remain temporary;
portable snapshot v1 does not transfer saved collections.

Alpha.4 uses schema 7; alpha.3 uses schema 6. Stop all Findrail processes and
back up the whole data directory, including WAL/SHM files when present, before
upgrading. An alpha.3 binary cannot open an upgraded schema-7 index.
[Installation, stopped backup and rollback](../build/package/INSTALL.txt).

Packages remain unsigned and unnotarized. macOS builds require macOS 13+.
OCR, semantic retrieval, private GitHub, team authentication and automatic
updates are not included. The optional VSIX remains version 0.1.0 for client
API v1; no Marketplace/Open VSX listing is claimed.

README screenshots remain identified as real alpha.3 captures. Documentation
updates after publication do not change this release's source revision,
tag or assets. Earlier release and candidate records remain historical evidence.
