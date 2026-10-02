# Configuration

The foundation uses command flags and platform data-directory defaults. There is
no TOML / YAML configuration parser yet; avoid treating an example as supported
runtime configuration.

```bash
findrail index --data-dir /private/findrail --max-bytes 1048576 /selected/notes
findrail search --data-dir /private/findrail --limit 10 --source SOURCE_ID "webhook"
findrail serve --data-dir /private/findrail --addr 127.0.0.1:7766
```

All flags precede positional arguments. Future configuration needs a schema
version, explicit source allowlists, validated resource limits, and documented
flag precedence. Credentials belong in a platform vault, not example files.
