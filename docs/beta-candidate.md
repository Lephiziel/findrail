# Beta.1 candidate rehearsal (not a release)

`0.1.0-beta.1` is a limited desktop beta candidate on schema 8. TASK 35/A sets
`build/package/VERSION` to `0.1.0-beta.1` so candidate packages carry their
intended version; that metadata change does not create a tag or release, or
authorize publication. Pull requests and pushes to `main` remain read-only even
when `VERSION` equals `CANDIDATE_VERSION`. Unknown beta/stable versions fail
closed.
The canonical platform, readiness, and owner-decision policy is
[beta support](beta-support.md); the offline evidence handoff is specified in
[beta evidence](beta-evidence.md).
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

The explicit performance workflow must be dispatched with the same full source
SHA and `candidate_version=0.1.0-beta.1`. It packages the candidate with that
version through the production packager and validates the repository's
`CANDIDATE_VERSION`; the alpha.5 comparison binary is built separately from the
pinned baseline source. Do not use a TASK21 series or TASK23 soak whose candidate
binary hash/version differs from the beta native Linux artifact.

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

Matching beta.1 human outcomes must be tied to the exact frozen candidate and
archive. Windows first-run uses the existing `windows_first_run` validator kind;
the owner's 35/C Windows VM is a plan, not a completed report. Apple Silicon
first-run and voluntary-use records use the existing `apple_silicon_first_run`
and `voluntary_usage` kinds. Linux first-use observations are separate and do
not add a readiness gate. The earlier TASK 30 acceptance is valid only for its
recorded `b593c70258d4fcc8491d07247dff2004b9580025` SHA/bytes; it is not beta.1
evidence for this or a later candidate. Automated browser/native CI is not a
human report. Missing human evidence remains pending; malformed, failed, or
mismatched supplied evidence blocks readiness. Recovery, 10k/100k scale,
performance series, and soak are technical evidence and cannot be waived as
missing people. `final_release` remains false. See the
[readiness distinction and permitted limited-beta decision](beta-support.md#three-different-decisions).

Build manual first-run kits only from the actual, byte-verified beta release
asset bundle, not from an edited alpha.5 manifest. At this revision the existing
kit generator covers Windows and Apple Silicon; TASK 32 adds the Linux amd64
observation kit as a separate, non-readiness observation. After downloading the
`release-assets-<full-sha>` Actions artifact into `/abs/evidence/assets`, the
existing Windows/Apple examples are:

```bash
python3 scripts/acceptance_kit.py --assets-dir /abs/evidence/assets \
  --target windows-amd64 --delivery-method 'Actions artifact <run URL>' \
  --output /abs/evidence/windows-beta-kit.zip
python3 scripts/acceptance_kit.py --assets-dir /abs/evidence/assets \
  --target darwin-arm64 --delivery-method 'Actions artifact <run URL>' \
  --output /abs/evidence/apple-silicon-beta-kit.zip
```

The generated reports start at `not_run`; they do not constitute OS or usage
evidence. Share kits only through the candidate's actual artifact channel and
collect voluntary matching reports separately.

After the successful exact-SHA technical handoff, the owner must separately
decide whether to authorize publication. TASK 34's explicit-publisher protection
is integrated; only its owner-only promotion path can publish after revalidating
the accepted evidence. This document and the TASK 35/A version change do not
authorize that action. The current download remains alpha.5 until actual beta
publication and independent verification of its public bytes.
Any planning target around mid-December for 1.0 is a planning assumption, not a
promised release date.
