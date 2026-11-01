# Alpha.4 desktop acceptance

This is the final native acceptance sheet for the alpha.4 release preparation.
It is separate from the owner's already completed alpha.3 Word/repository
indexing check. The previous candidate at
`ab5565384d363594e12aa6795adc3e09872bb359` passed
[run 38506630996](https://github.com/Lephiziel/findrail/actions/runs/38506630996):
five native packages, browser journey, installed VSIX, and upgrade/backup checks.
Native opening there uses controlled adapters, not a real external viewer.

**Current status: owner acceptance reported passed on 2026-10-14.**
The owner confirmed that everything in the supplied short check works. This is
an owner-reported result, separate from the automated controlled-adapter checks.
The release PR can proceed through its exact-head CI and intentional publication.

## Five-minute owner check

Use the archive for your actual OS from the **Alpha release candidate** workflow
on this PR. The artifact `release-package-<sha>-<target>` contains the archive
and `verification.json`; extract the verified archive into a folder you own.
Confirm `BUILD-INFO.json` has version `0.1.0-alpha.4`, schema `7`, target for
your computer, and the exact candidate SHA. This is not a public release yet.

1. **Prepare only fictional files.** A Markdown note containing a unique word
   such as `alpha4check`, a text PDF and a DOCX are enough. Use a new, separate
   data directory, or first stop all Findrail processes and back up the complete
   existing directory. Do not upgrade your sole alpha.3 index without a backup.
2. **Install and launch from the OS menu.** Run the per-user installer in
   `launcher/linux/`, `launcher/macos/` or `launcher/windows/`, then launch
   Findrail from the menu/app/shortcut. Add the fictional folder and search.
   Launch again: it must reopen the same running app, rather than create a new
   server. Close just the browser tab and relaunch; the app must still be running.
   Choose **Quit Findrail** and confirm the process stops.
3. **Open a real original.** Start the candidate with originals explicitly
   enabled and a separate data directory, for example on Linux:
   `./findrail-desktop --data-dir ./findrail-alpha4-test --enable-originals`.
   Add the same fictional folder. Use **Открыть оригинал** on a text/PDF/DOCX
   result, and **Открыть папку**. Confirm your real editor/viewer and containing
   folder open. A PDF's exact initial page depends on the viewer.
4. **Save and restore.** In Preview select a passage, add it to a named
   collection, add a note and press **Save**. Quit Findrail, reopen it with the
   same data directory, choose the saved collection and check its text/note.
5. **Remove only the integration.** Quit and run the per-user uninstall script.
   Confirm its menu/app/shortcut entry is removed and the data directory remains.
   Keep the extracted app folder until the report is complete.

Menu installers use the normal OS data location, so use an OS account with no
existing Findrail index or back up that stopped directory before step 2. A direct
`--data-dir` launch tests isolation and lifecycle but does not establish the
menu's first-run experience. Do not disable system-wide security protections for
the unsigned alpha.

## Report

Fill only after doing the check. A failure needs the exact step and a synthetic
reproduction; keep private paths and document contents out of public reports.

| Field | Result |
|---|---|
| Date, OS/version, CPU | 2026-10-14; exact OS version and CPU were not separately reported |
| Candidate supplied for this check | `a0d44f53015287c50881bfd65be93d5f6aaf22cf`; Linux amd64 alpha.4 archive, schema 7; package identity verified before handoff |
| Menu install / first launch | Passed according to the owner's overall short-check report |
| Repeat launch / closed-tab reuse | Passed according to the owner's overall short-check report |
| Protected Quit | Passed according to the owner's overall short-check report |
| Original in real viewer / containing folder | Passed according to the owner's overall short-check report |
| Collection text and note after restart | Passed according to the owner's overall short-check report |
| Uninstall preserves data | Passed according to the owner's overall short-check report |
| Security prompt / viewer limitations observed | No failure reported; exact viewer and security-prompt details were not supplied |

The owner accepted the supplied short check without a separate per-step log.
This does not establish manual GUI coverage on every platform; macOS and Windows
GUI acceptance are not independently recorded here. The
repository's [desktop guide](desktop-launcher.md) calls for native GUI acceptance;
cross-compilation and controlled adapters are not substitutes.

## Publication after acceptance

With owner acceptance recorded, mark the release PR ready and merge it only after
the exact head's required checks pass. Changing `build/package/VERSION` to
`0.1.0-alpha.4` on main intentionally enables publication after all release gates.
The main release run's source revision and generated bytes are authoritative;
PR preview archives are not the final public downloads.

After publication, verify all eight assets, `release-manifest.json` revision,
schema 7 / API 1 / VSIX 0.1.0, complete checksums and extracted binary versions.
Then follow a fresh download through startup/search and check that alpha.3's
historical tag and eight assets remain unchanged.
