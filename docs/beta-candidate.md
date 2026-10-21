# Beta.1 candidate rehearsal (not a release)

`0.1.0-beta.1` is an explicitly supported **preparation-only** candidate on
schema 8. It does not change `build/package/VERSION` (`0.1.0-alpha.5`), create a
tag/release, or authorize publication. Unknown beta/stable versions fail closed.
The contract remains alpha.3/schema 6, alpha.4/schema 7, alpha.5/schema 8, and
beta.1/schema 8; Client API is 1, VSIX is 0.1.0, with the same five native
targets and eight release-staging files. Historical manifests and release
verification continue to describe the version they were built for.

Run **Actions → Package rehearsal (no release)** with the exact current `main`
SHA and `candidate_version=0.1.0-beta.1`, or use the Alpha release candidate
workflow with `publish=false`. The PR/main candidate selector is
`build/package/CANDIDATE_VERSION`. The rehearsal builds and verifies the real
native package, then runs the extracted journey. It downloads the public
alpha.5 Linux archive, verifies SHA-256
`78885728107e26be6db18f93c0f94ee6af92b585f00aea21dd93ba9e19a5a8f0` and checks
the recorded alpha.5 source revision before exercising same-schema compatibility.
The candidate must open an index created by that published binary without
migration and preserve indexed content, provenance, collections, reopen and a
stopped backup/restore. This is not a promise of downgrade support. The
candidate acceptance scripts consume extracted binaries; they do not silently
build a replacement application.

Product scope is unchanged: local-first search, existing extraction and query
limits, read-only loopback/API boundaries, existing provenance and rollback
semantics. No OCR, semantic retrieval, new connectors, cloud/team/auth,
updater, signing service, mandatory AI or telemetry is part of this candidate.

Existing measured performance is not represented as a beta measurement. The
published alpha.5 record reports 100k-document warm HTTP search p95 of
201.291 ms in its pre-release run and 146.797 ms in a later run; the latter used
ten warm requests per run and is not evidence of an optimization or stable SLO.
Both are historical measurements tied to their recorded source/corpus/environment
in the [alpha.5 verification record](release-alpha5-verification.md) and
[resource baseline](resource-baseline.md). No beta binary measurement is claimed
here.

Human evidence remains pending for TASK30: actual Windows first-run/SmartScreen
and GUI experience; macOS Apple Silicon Finder/Gatekeeper first-run; independent
retrieval usefulness and return-use reports tied to the exact candidate/archive;
and human crash-recovery observations where requested. Automated browser/native
CI is not a substitute. Missing reports remain pending; malformed or failed
provided evidence blocks readiness. TASK21 resource-series and TASK23 soak
reports are supplemental technical evidence, not synthetic substitutes for
manual use. `final_release` remains false.

After a successful rehearsal, the owner must separately decide whether to change
the shipped version and authorize a future release workflow. This document does
not authorize that action. Any planning target around mid-December for 1.0 is a
planning assumption, not a promised release date.
