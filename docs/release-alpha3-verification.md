# Findrail alpha.3: published release verification

Verified on 2026-10-13 after publication at 2026-10-13T07:36:34Z.

- Release: [Findrail v0.1.0-alpha.3](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3); public prerelease, not a draft.
- Tag: `v0.1.0-alpha.3`; dereferenced commit `d5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb`.
- Production gates: [exact release-commit workflow](https://github.com/Lephiziel/findrail/actions/runs/38363028553).
- Application / extension / client API / schema: `0.1.0-alpha.3` / `0.1.0` / `1` / `6`.
- Public asset downloads, exact eight-file set, hashes, sizes, BUILD-INFO and VSIX contract passed.

## Downloaded public assets

| File | Size (bytes) | SHA-256 |
|---|---:|---|
| [SHA256SUMS.txt](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/SHA256SUMS.txt) | 724 | `6a0f394563765af692ad24bc44a77ce4af5664fb3ee2b421c314e7b28e5b348b` |
| [findrail-readonly-0.1.0.vsix](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail-readonly-0.1.0.vsix) | 18,033 | `06e3fac84acea06ca961f9592e114d6cce315f979d8b59369d2885437766d95d` |
| [findrail_0.1.0-alpha.3_darwin-amd64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail_0.1.0-alpha.3_darwin-amd64.tar.gz) | 6,284,782 | `73377481be4b81cf4632f5695308e0f6224e91af89489d9c67fec569dccc01c9` |
| [findrail_0.1.0-alpha.3_darwin-arm64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail_0.1.0-alpha.3_darwin-arm64.tar.gz) | 5,928,031 | `299201dbb909152e60989a4bea2984f0b0b8b54b0ffc6b562200f2728b17fce1` |
| [findrail_0.1.0-alpha.3_linux-amd64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail_0.1.0-alpha.3_linux-amd64.tar.gz) | 6,271,390 | `b8e9d606e6487745a9f96d4453114e7ef6ec74fcde242a7da20a059462b3b9e9` |
| [findrail_0.1.0-alpha.3_linux-arm64.tar.gz](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail_0.1.0-alpha.3_linux-arm64.tar.gz) | 5,794,219 | `6a4a2c7aa16f290d25d363cc92ad0ec2669df4c5f23f1d5e649d121a66302dd5` |
| [findrail_0.1.0-alpha.3_windows-amd64.zip](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/findrail_0.1.0-alpha.3_windows-amd64.zip) | 15,526,498 | `f9fe63960e104845727260349c79e0adef3557ee46e17a49effca974639b5a72` |
| [release-manifest.json](https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.3/release-manifest.json) | 1,612 | `27a7d328e15259cef3563f2ae1edec26df38b515a23b6f6647021f5d331b4d16` |

`SHA256SUMS.txt` covers the five archives, VSIX and manifest. Its own digest is
recorded above from the public download. The manifest source revision and every
archive's clean BUILD-INFO match the dereferenced tag commit. Native results
below come from that same production workflow, not from the earlier candidate.

## Verification scope

| Check | Result / scope |
|---|---|
| [source-checks / check (windows-latest)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182877510) | Passed at the release commit |
| [validate-source](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182877706) | Passed at the release commit |
| [native-packages (windows-amd64, windows-latest)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182878757) | Passed at the release commit |
| [source-checks / check (ubuntu-latest)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182878765) | Passed at the release commit |
| [native-packages (darwin-arm64, macos-14)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182878852) | Passed at the release commit |
| [source-checks / ui](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182879045) | Passed at the release commit |
| [native-packages (linux-amd64, ubuntu-latest)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182879116) | Passed at the release commit |
| [native-packages (darwin-amd64, macos-15-intel)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182879252) | Passed at the release commit |
| [source-checks / race](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182879315) | Passed at the release commit |
| [installed-vsix](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182879548) | Passed at the release commit |
| [source-checks / check (macos-latest)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182879681) | Passed at the release commit |
| [packaged-browser-journey](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182880337) | Passed at the release commit |
| [native-packages (linux-arm64, ubuntu-24.04-arm)](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115182918757) | Passed at the release commit |
| [aggregate-and-verify](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115183501017) | Passed at the release commit |
| [reusable-final-verification / verify-assets](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115183621675) | Passed at the release commit |
| [release-state](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115183676110) | Passed at the release commit |
| [publish](https://github.com/Lephiziel/findrail/actions/runs/38363028553/job/115183727479) | Passed at the release commit |
| Public Linux amd64 archive | Safely extracted outside the checkout; version and CLI smoke passed |
| Owner local run | Startup, Word-folder indexing/search/Preview and repository indexing/search reported successful |
| Owner export/restart/PDF-page checks | Not separately reported; automated Chromium coverage above |
| Manual macOS Finder/Gatekeeper and Windows GUI | Not run; native CI does not imply GUI certification |

The owner smoke predates the final metadata commit. No additional manual step
or independent binary identity is inferred. Full production gates rebuilt the
final commit; no candidate archive was renamed or reused as a release archive.

Production attempt 2 passed. The first Windows source-check attempt timed out
waiting for a listener in a three-second test window; its native Windows package
checks passed. The retry used the same release SHA and completed the full source
checks before publication. The failed attempt remains visible in the workflow
history; no runtime change or bypass of the release gates was made.

## Distribution and limitations

Archives are unsigned and unnotarized. Index text and paths are plaintext.
OCR, semantic retrieval, private GitHub, automatic updates and desktop launching
are not included. Public GitHub refresh is manual. Collections clear on tab reload.
The separate VSIX needs VS Code 1.95+ and a running Findrail API-v1 server; no
Marketplace/Open VSX listing is claimed. Connector tooling remains experimental.

Documentation promotion follows these verified public URLs and keeps VERSION
at alpha.3. Earlier alpha.2 releases and dated candidate reports remain history.
No social posts, editorial submissions or messages were sent by this release task.
