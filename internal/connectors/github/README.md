# GitHub connector design

Planned; no GitHub ingestion is implemented yet.

The first adapter reads issues and discussions from explicitly selected public
repositories. A later private-repository mode needs a credential vault and
repository scopes before release. It never posts comments or changes repositories.

- Stable identity: repository node ID and issue / discussion node ID, not title.
- Provenance: canonical GitHub URL, repository, author, creation / update times.
- Rate limits: bounded retries, cancellation, and visible sync health.
- Inventory: missing pagination, revoked access, and rate exhaustion are failures,
  not evidence that unseen documents were deleted.
- Incremental sync: cursors and explicit tombstones need a new protocol; do not
  pass partial pages to the current complete-inventory `Scan` contract.

See [connector semantics](../../../docs/connectors.md) and
[acceptance criteria](../../../docs/implementation-plan.md).
