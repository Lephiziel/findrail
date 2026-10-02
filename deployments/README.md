# Running Findrail

The supported foundation is one local process and a private index directory:

```bash
findrail serve --data-dir /private/findrail --addr 127.0.0.1:7766
```

No external database is needed. The HTTP listener rejects non-loopback addresses.
There is no current Docker / Kubernetes deployment or remote authentication
implementation. A future operated service needs authentication, per-user source
authorization, encrypted transport, backup / restore, migrations, and a tested
threat model before it is exposed to other computers.

Background desktop service integration will be designed with the file-watching
milestone and must keep source access and shutdown behaviour visible to users.
