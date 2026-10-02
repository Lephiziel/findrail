# Alpha distribution

`python3 scripts/package.py` builds five CGO-free archives and SHA-256 checksums
in `dist/`. Use `--go /path/to/go` and optionally repeat `--target TARGET` to
select targets. The version comes from `VERSION`; `--version` overrides it.
Archives include the executable, installation steps, Apache license, notice,
Go license and dependency license notices. This script uses Go's module cache
and may download build dependencies; the installed application does not.

The alpha release workflow runs after successful CI on a push to `main`, builds
from that exact commit and creates a prerelease only when the version tag does
not already exist. Change `VERSION` for a new release. Existing release assets
are never replaced by this workflow. No signing or notarization is claimed.
