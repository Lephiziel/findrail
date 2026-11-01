# GitHub connectors

This package implements bounded snapshots of text files from public GitHub
repositories. It resolves one ref to a commit and prepares an in-memory archive
inventory; host storage publishes it atomically.

Issues and discussions remain future separate adapters. A private-repository
mode needs a credential vault and repository scopes before release. Findrail
never posts comments or changes repositories.

- Stable identity: repository node ID and issue / discussion node ID, not title.
- Provenance: canonical GitHub URL, repository, author, creation / update times.
- Rate limits: bounded retries, cancellation, and visible sync health.
- Inventory: missing pagination, revoked access, and rate exhaustion are failures,
  not evidence that unseen documents were deleted.
- Incremental sync: cursors and explicit tombstones need a new protocol; do not
  pass partial pages to the current complete-inventory `Scan` contract.

See [connector semantics](../../../docs/connectors.md) and
[acceptance criteria](../../../docs/implementation-plan.md).
