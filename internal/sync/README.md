# Synchronization design

Planned; re-running `findrail index` is the current refresh mechanism.

This module will own a durable job queue, per-source scheduling, watcher event
debouncing, retries with backoff, cancellation, and source health. Search must
stay usable while jobs run. Start with one storage writer and measure read
contention before changing the storage topology.

Watcher events trigger reconciliation rather than unconditional deletion. Remote
adapters use validated cursors and explicit tombstones. A successful partial
batch must never masquerade as a complete inventory. Persist a cursor only in
the same transaction that publishes the corresponding document changes.

Tests must cover overflowed watcher queues, interruption, retry, duplicate events,
access revocation, and deletion propagation.
