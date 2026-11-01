# Findrail Read-only Search for VS Code

This local UI extension retrieves bounded indexed text from an independently started Findrail source build. It does not start Findrail, access SQLite/original files, mutate sources, scan workspaces, persist queries/results, or make background requests. Its source selection is a client-side retrieval preference, **not** a server authorization scope; the ordinary HTTP server exposes the whole local index to local clients. This is not multi-user security.

Requires VS Code 1.95+ and a compatible source-built Findrail server (client API v1). Existing alpha.2 release archives do not advertise this compatibility. Start explicitly, for example `findrail serve --data-dir INDEX --no-sync` or `findrail start --data-dir INDEX --no-open`. Server must be on the local UI machine at loopback.

Commands: Connect, Choose Source, Search, Search Selection (query is shown for confirmation), Set Search Mode (Literal default / Advanced), Set Filters (format/path/title), Refresh Sources and Disconnect. Search is always constrained to the chosen source. Archive sources are frozen snapshots. PDFs show actual page evidence; title-only results start at page 0. DOCX has no page attribution. Preview is plaintext. Copy Location and Copy Markdown Citation are explicit commands.

Endpoint is a user/machine setting only (`findrail.endpoint`); only `127.0.0.1`, `localhost`, and `::1` HTTP URLs are accepted. Remote SSH/Containers windows use this extension on the local UI side and connect to that machine's Findrail; there is no automatic tunnel or remote forwarded connection. Restricted Mode is supported because commands do not execute workspace code/tasks. The extension cannot control other installed extensions or editor-level data handling. No polling: refresh sources/evidence explicitly.

## Development / installation

```sh
npm ci
npm run typecheck
npm test
npm run test:integration
npm run test:host
npm run package
code --install-extension findrail-readonly-0.1.0.vsix
# remove
code --uninstall-extension findrail-local.findrail-readonly
```

Use VS Code **Install from VSIX...** for Windows/macOS/Linux. Runtime requires VS Code and compatible Findrail only; Node/npm are development requirements. Publisher label is local and makes no Marketplace claim.
