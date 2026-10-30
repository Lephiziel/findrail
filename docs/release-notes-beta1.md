# Findrail 0.1.0-beta.1

## A limited desktop beta across Linux, Windows and macOS

The `0.1.0-beta.1` distribution keeps the same local desktop application and
functional core across its native builds. It is a limited desktop beta, not a
stable release or a v1 readiness claim. The release contract is schema 8,
Client API 1, and VSIX 0.1.0, and keeps all five native archives and all eight
release assets; see the
[platform support policy](beta-support.md) for the exact per-target boundaries.

- **Linux amd64:** primary beta path. Any manual outcome must be tied to the
  exact candidate/archive tested; no claim covers every distribution or desktop
  environment.
- **Windows amd64:** primary target. The planned owner Windows VM first-run is
  not yet a result; manual status remains pending until a matching report is
  reviewed. A passed report will describe only its recorded VM, Windows build,
  and completed scenarios.
- **Linux arm64, macOS Apple Silicon, and macOS Intel:** experimental targets.
  Candidate-native CI is required, but no independent manual result is asserted
  here. Linux amd64 evidence does not establish Linux arm64 coverage, and
  macOS Finder/Gatekeeper behavior is not inferred from CI.

At the time these notes were frozen, no matching independent beta.1
Windows or Apple Silicon first-run or voluntary-use report had been accepted.
The planned Windows VM first-run remains pending. Historical alpha.5 Linux
observations are not beta.1 evidence. Automated package, browser, or installer
checks are not human first-run or voluntary-use reports. Matching observations
must be tied to the exact candidate and archive.
If matching evidence is accepted after these notes are frozen, update the
publication-notes copy and hash and review them against that evidence before use.

## Product and operational limits

- Search is local and lexical. Scanned/image-only PDFs are skipped; OCR is not
  included. DOCX previews expose extracted body text, not Word layout or page
  numbers.
- The local index stores extracted text and paths as plaintext and is not
  encrypted at rest. Back up the whole data directory while Findrail is
  stopped before upgrading; schema 8 rollback requires the complete stopped
  pre-upgrade backup and matching old binary.
- Builds are unsigned and macOS packages are not notarized. Windows security
  prompts and macOS Finder/Gatekeeper download behavior require honest,
  platform-specific observation; do not disable operating-system protections
  globally.
- Findrail does not upload selected documents or add runtime telemetry. OCR,
  semantic search, private GitHub, hosted/cloud/team authentication, automatic
  updates, and signing/notarization services are outside this candidate.
- Experimental labels describe evidence coverage, not reduced functionality
  and not a ban on downloading an archive.
