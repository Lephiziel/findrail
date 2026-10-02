# Packaging

Current developer build:

```bash
CGO_ENABLED=0 go build -trimpath -o bin/findrail ./cmd/findrail
```

Cross-compilation uses ordinary `GOOS` and `GOARCH`; for example:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/findrail.exe ./cmd/findrail
```

Published release artifacts, installers, signed checksums, Homebrew manifests,
and winget manifests are planned. Publish only after testing the complete search
workflow on each supported OS. A successful cross-compilation alone does not
establish runtime support.

Use Apache-2.0 notices and dependency license information in distributed bundles.
No release or installer exists merely because this packaging directory exists.
