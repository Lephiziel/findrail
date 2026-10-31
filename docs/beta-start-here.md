# Desktop first use and optional feedback

**Intended audience:** Linux amd64 users trying Findrail's desktop workflow.
This page does not announce a beta: beta packaging is being prepared, and no
public beta download is available yet. The current public download remains
[Findrail 0.1.0-alpha.5](https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.5).
Windows and macOS downloads remain available too; experimental builds are not a
restriction on trying the product or sending feedback.

## Download, verify, and try

Download the Linux amd64 archive and `SHA256SUMS.txt` from the same release.
Before extracting, run `sha256sum` on the exact archive and compare the complete
64-character result with its row in `SHA256SUMS.txt`. Stop if it differs. Extract
the archive, enter its folder, and read its included `INSTALL.txt`.

Start the desktop launcher from the extracted folder:

```sh
./findrail-desktop
```

In the local interface choose **Sources → Add folder** and select only a folder
you want Findrail to index. Search a word you remember, open **Preview**, and
check the matching passage and source. Text PDF results include page attribution.
For saved collections, add a passage, name the collection, add any note, and
press **Save**.

Closing the browser tab only hides the interface. **Quit Findrail** stops the
local desktop server. To test a saved collection, choose Quit and launch again
with the same data directory. The separate `findrail demo` is temporary and
read-only; its in-tab collection does not persist across restart.

## Upgrade and backup

Before upgrading, stop every Findrail process using the index and copy the **whole
data directory**, including SQLite WAL/SHM files when present. By default it is
`$XDG_DATA_HOME/findrail` or `~/.local/share/findrail`; use your configured
`--data-dir` if different. For example:

```sh
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/findrail" # replace with your configured --data-dir if different
BACKUP="$HOME/findrail-backup-$(date +%Y%m%d-%H%M%S)"
cp -a "$DATA_DIR" "$BACKUP"
```

Keep the backup outside the directory being copied. Read the new archive's
`INSTALL.txt` for its exact schema and rollback instructions. Migrations are
forward-only: do not open an upgraded index with an older binary. Restore a full
stopped pre-upgrade backup with its matching binary if rollback is needed.

The included Linux per-user installer adds a menu entry; its matching
uninstaller removes that entry, not the application archive or index. No Go,
Python, Docker, token, or app account is needed to run Findrail.

## Privacy and optional feedback

Findrail indexes only folders you choose. The index contains extracted text and
source paths in local, unencrypted storage. The app has no runtime telemetry and
does not automatically send reports. Treat the data directory like your original
documents.

Feedback is optional. You can try the
[desktop first-use form](https://github.com/Lephiziel/findrail/issues/new/choose)
on Linux, Windows, or macOS; you do not need to look up a Git SHA or provide
private paths, queries, documents, indexes, or logs. A short sanitized outcome
or blocker is enough. You may instead keep a report privately and share only a
SHA-256 reference that you have checked. Public issue submission is not required.

For a later-use report, match the same candidate version/identity when known and
use an optional public issue URL or an operator-checked local `sha256:` reference.
Unknown SHA feedback remains useful qualitatively, but does not match release
evidence. First use does **not** count as `return_use: true`; mark it true only
after coming back later. If you choose to record `retrieval_one`, `retrieval_two`,
and `collection_restart`, preserve the actual `found`/`not_found` and
`restored`/`not_restored` outcomes; skipped tasks stay `not_tried` and
incomplete. Do not include the queries or document contents.
