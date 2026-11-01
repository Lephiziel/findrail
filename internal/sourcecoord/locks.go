// Package sourcecoord provides process-local, per-source operation exclusion.
// Persistent storage guards remain authoritative across separate processes.
package sourcecoord

import (
	"context"
	"errors"
	"sync"
)

type entry struct {
	semaphore  chan struct{}
	references int
}

// Locks coordinates scans and removals by source ID. Locks for different IDs
// are independent; waiting never holds the map mutex or a SQLite writer lock.
type Locks struct {
	mu      sync.Mutex
	entries map[string]*entry
}

func New() *Locks { return &Locks{entries: make(map[string]*entry)} }

func (l *Locks) Acquire(ctx context.Context, sourceID string) (func(), error) {
	if sourceID == "" {
		return nil, errors.New("source ID is required")
	}
	l.mu.Lock()
	e := l.entries[sourceID]
	if e == nil {
		e = &entry{semaphore: make(chan struct{}, 1)}
		l.entries[sourceID] = e
	}
	e.references++
	l.mu.Unlock()
	select {
	case e.semaphore <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-e.semaphore; l.release(sourceID, e) }) }, nil
	case <-ctx.Done():
		l.release(sourceID, e)
		return nil, ctx.Err()
	}
}

func (l *Locks) release(sourceID string, e *entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.references--
	if e.references == 0 {
		delete(l.entries, sourceID)
	}
}
