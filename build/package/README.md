# Package builds

`python3 scripts/package.py` builds five CGO-free archives and SHA-256 checksums
in `dist/`. Use `--go /path/to/go` and optionally repeat `--target TARGET` to
select targets. The version comes from `VERSION`; `--version` overrides it.
Archives include the executable, installation steps, Apache license, notice,
Go license and dependency license notices. This script uses Go's module cache
and may download build dependencies; the installed application does not.

Pull requests and pushes to `main` build and verify candidate packages; they do
not create or modify a release or tag, even when `VERSION` matches
`CANDIDATE_VERSION`. Beta.1 publication requires a separate owner-only promotion
after the exact candidate and evidence have been reviewed. The currently
published download remains alpha.5 until that promotion is completed and its
public bytes are verified. No signing or notarization is claimed.
