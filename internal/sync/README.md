# Local source refresh

Implemented in `sync.go`: registered-source discovery, per-source workers,
fsnotify directory watches, bounded debounce, periodic full inventories, retry,
cancellation and in-memory health. Native events trigger reconciliation rather
than unconditional deletion. Watch setup failures fall back to periodic scans.

Separate SQLite readers keep searches on the previous committed snapshot during
writes. Refresh cannot register a source, so forgetting it prevents resurrection.
Restart rebuilds workers and refreshes complete inventories. Durable remote jobs,
cursors, access revocation and resumable batches remain future work.

Tests cover native and polling update / rename / nested creation / deletion,
forgotten sources, cancellation and missing-root recovery.
