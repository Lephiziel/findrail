# alpha.3 post-release promotion checklist (for task 06)

Completed on 2026-10-13 after the authorized [alpha.3 release](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.3)
was published and all eight public files were downloaded and checked. The tag
points to `d5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb`. See [asset hashes, URLs and checks](release-alpha3-verification.md).

## Verify first

- [x] Confirm the actual `v0.1.0-alpha.3` release exists and is the expected
  prerelease; verify tag, full source SHA, release notes and publication time.
- [x] Download each listed Linux amd64/arm64, macOS amd64/arm64, and Windows
  amd64 archive from that release. Verify exact basenames against that same
  release's `SHA256SUMS.txt`; inspect embedded INSTALL, BUILD-INFO and notices.
- [x] Verify the separate VSIX and `release-manifest.json` from that same release;
  compare the exact eight-file inventory, all asset hashes/sizes, manifest source
  revision and the tag's dereferenced full commit SHA.
- [x] Record native packaged execution separately from cross-compilation. Check
  actual browser smoke, installed editor/VSIX, and manual macOS
  Finder/Gatekeeper independently; leave any unrun result marked unrun.
- [x] Verify the VSIX API v1 requirements and the actual release asset URL.
  This release distributes the VSIX directly; its publisher does not publish
  Marketplace or Open VSX listings. No listing is inferred from the local VSIX.

## Completed documentation promotion

1. In `README.md`, change the current-release label and clearly name alpha.3 as
   published. Add version-specific archive and checksum links only by copying
   verified URLs from the release; retain alpha.2 as the previous available
   version rather than deleting its links.
2. In `docs/launch/press-kit.md`, change the release status and matrix column
   from “current source build / candidate” to “published alpha.3” only for
   verified features. Keep a separate VSIX row; mark Marketplace/Open VSX
   unpublished unless those exact listings are verified.
3. In `docs/launch/README.md` and `docs/ru/overview.md` / `docs/ru/launch.md`,
   replace source-only wording with released wording only for the features in
   the verified package. Keep limitations, schema-6 rollback guidance and
   platform/manual-test caveats.
4. Update the embedded `build/package/INSTALL.txt` status before building the
   final release archives; they must describe the version in their BUILD-INFO.
   Promote `docs/release-notes-alpha3.md` to the actual release notes after
   comparing it with shipped contents and correcting any differences. Keep
   candidate rehearsal evidence dated and distinct from release certification.
5. Check every changed link and asset URL against the public release. Do not
   create a link for a missing architecture, checksum, Marketplace, or Open VSX
   listing. Update the checklist with the verified SHA, URLs, and evidence.

This records the completed task-06 promotion. VERSION stays at alpha.3; the
follow-up documentation does not replace release assets or move its tag. Manual
Finder/Gatekeeper and Windows GUI checks remain not run. No external messages
or submissions were sent.
