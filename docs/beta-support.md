# Beta.1 platform support and release policy

This page is the canonical platform and readiness policy for the prospective
`0.1.0-beta.1` limited desktop beta. It describes preparation, not a published
product. Until a separately authorized publication is complete, the current
download remains [alpha.5](release-alpha5-verification.md). The candidate
contract remains schema 8, Client API 1 and VSIX 0.1.0; this policy does not
change `VERSION`, the publisher, or any release assets.

## Platform promises

All supported operating systems receive the same functional application core.
Manual coverage is reported per exact candidate and platform; it does not
change which archives are built or offered. Missing volunteers do not block
policy/code preparation; they leave only the corresponding manual evidence
pending.

| Target | Limited beta.1 promise | Manual coverage boundary |
|---|---|---|
| `linux-amd64` | Primary beta path. | Any manual outcome applies only to the candidate/archive actually checked. It does not promise every Linux distribution, desktop environment, browser, or installation method. A historical alpha report is not beta evidence. |
| `linux-arm64` | Experimental. Its matching candidate must pass the native CI gate; lack of an independent tester does not block preparation. | No manual result is implied by an `amd64` report. |
| `windows-amd64` | Primary target. The owner plans the manual first-run in a Windows VM at 35/C; manual evidence stays pending until a matching passed report is validated. | A matching passed report supports only its named scenarios in its recorded VM and Windows build. A failed/invalid report blocks; it does not certify physical devices or other Windows builds. |
| `darwin-arm64` | Experimental. Its matching candidate must pass the native CI gate. | Finder/Gatekeeper manual results are not claimed until a matching report exists. |
| `darwin-amd64` | Experimental. Its matching candidate must pass the native CI gate. | No manual result is claimed until a matching report exists. |

The full native matrix stays mandatory: Linux amd64/arm64, macOS amd64/arm64,
and Windows amd64. The release remains exactly eight assets: those five native
archives, `findrail-readonly-0.1.0.vsix`, `release-manifest.json`, and
`SHA256SUMS.txt`. Cross-platform scope is not permission to drop four archives,
skip a native gate, or transfer one architecture's manual result to another.
See the [candidate](beta-candidate.md) and [release pipeline](release-pipeline.md)
contracts for the existing package and verification mechanics.

## Three different decisions

### 1. Software acceptance

The exact candidate must pass its required source checks, all five native
package/lifecycle jobs, packaged browser journey, installed VSIX checks, exact
asset aggregation and reusable final verification. Linux recovery must also
pass against the extracted Linux amd64 candidate. A gate that failed, was
skipped where mandatory, or used another SHA/version is not accepted. A target's
experimental label does not waive its native CI gate.

The existing workflow and validators remain authoritative; this document does
not add a parallel acceptance engine. `scripts/readiness.py`'s
`software_status` is not a summary of every release gate: it reflects strict
candidate validation plus recovery, while scale and human evidence affect the
separate readiness result. Never use one green field to hide another failed or
invalid result.

### 2. Readiness completeness

Keep the existing `scripts/readiness.py` output and semantics. Missing evidence
is `pending`; a supplied malformed, failed, duplicate, inconsistent, or
candidate-mismatched record is `blocked`/`invalid`, not pending. For the final
technical handoff, recovery, individual 10k and 100k scale evidence, and all
required CI gates must pass. A missing scale or recovery result is not covered
by limited scope.

With matching technical evidence but no human reports, the three human
categories remain explicitly pending: `windows_first_run`,
`apple_silicon_first_run`, and `voluntary_usage`. Linux first-use observations
from TASK 32 are useful but are not a new mandatory readiness kind. Blank kits
or generated reports whose cases remain `not_run` are not evidence. The
classifier continues to report `final_release: false`; full readiness
(`eligible-for-owner-decision`) is not publication authorization.

### 3. Owner decision for a limited beta

After all technical gates pass, the owner may separately decide whether the
candidate is sufficient for a *limited* desktop beta while only the three human
categories above remain pending. This is a narrow owner exception, not a change
to the validator: pending stays pending, and scope does not turn it into a
passed human report. Any failed/invalid/mismatched evidence, pending
scale/recovery, missing mandatory CI, or other technical failure remains a
blocker and cannot be waived by calling the release limited.

Zero independent users can be enough to prepare a decision card; it is not
evidence of usefulness, retention, ordinary use, or full beta/v1 readiness.
The owner must decide whether the exact candidate and its stated limitations are
enough for that distribution. The unfilled
[owner-decision card template](beta-owner-decision-template.md) is a format, not
approval, proof of identity, or a source of write permission.

For Windows, use the existing `windows_first_run` kind and readiness validator.
The report must match the exact candidate SHA/version and Windows archive hash;
record VM, Windows build, browser/download source, and observed security prompts
in the existing bounded report notes. The operator must confirm the report came
from a real observation. Do not infer it from CI or Wine. Linux observations
remain their own TASK 32 kind and never satisfy or substitute for Windows,
Apple Silicon, or voluntary-use evidence.

## Preparation, freeze, decision, and publication

1. **Prepare the policy and tooling.** TASK 31–34 are pre-beta work. TASK 34
   must be integrated before TASK 35/A changes `VERSION`. The candidate workflow
   now leaves publication disabled for every PR, fork, main push, and ordinary
   dispatch; a matching candidate and `VERSION` never select a publisher. Only
   the explicit owner-only canonical-main beta promotion path can write, and
   only after it revalidates the exact accepted handoff. Do not change `VERSION`
   as part of this policy task.
2. **Freeze one candidate.** After the required pre-beta work and TASK 34 are
   merged, TASK 35/A may prepare the version change and finalize
   [`release-notes-beta1.md`](release-notes-beta1.md). Freeze the resulting
   exact main SHA. TASK 35/B runs the full canonical candidate workflow and the
   final heavy measurements once against that SHA, then creates the verified
   read-only handoff described in [beta evidence](beta-evidence.md). Evidence
   from an earlier SHA is not carried forward. The accepted TASK 30 evidence
   applies only to its recorded `b593c70258d4fcc8491d07247dff2004b9580025`
   candidate bytes, not a later candidate.
3. **Refresh human intake without remeasuring.** If matching voluntary
   Windows/Linux reports arrive after the technical handoff, TASK 33 may
   revalidate intake/readiness against the same technical bundle and binary.
   It preserves technical run IDs and hashes, updates only the human/readiness
   and outer handoff material, then produces a new handoff digest for the owner.
   A changed digest requires a fresh review; it does not trigger another scale,
   series, or soak run.
 4. **Make a separate owner decision.** Show the owner the exact candidate SHA,
    accepted root handoff artifact digest, all eight asset names/sizes/SHA-256
    values, pending categories, known limitations, and exact release-note
    body/hash. Keep the filled decision record outside the product repository.
    No workflow JSON, generated card, or policy statement is itself the owner's
    authorization.
5. **Publish only through the explicit publisher.** Only after that decision
   may TASK 34's explicit path publish the approved candidate. Before that
   point, beta.1 is prospective preparation; no beta tag, release, download
   links, or public availability is claimed. Publication is a prerelease, not
   broad/stable/v1 publication, and the publisher must use the reviewed notes
   body/hash and exact approved asset inventory.
6. **Verify public bytes independently.** After publication, download all eight
   public assets and compare exact names, sizes and SHA-256 values with the
   approved inventory. Recheck `SHA256SUMS.txt`, manifest identity, archive
   BUILD-INFO and VSIX identity using the existing release verifier. A
   successful upload alone is not proof that public bytes match.

These phases keep policy, software acceptance, readiness, the owner's decision,
and publication distinct. They do not claim broad platform coverage, stable
quality, or v1 readiness.
