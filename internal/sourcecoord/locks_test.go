package sourcecoord

import (
	"context"
	"testing"
	"time"
)

func TestLocksSerializeSameSourceButNotDifferentSources(t *testing.T) {
	locks := New()
	releaseFirst, err := locks.Acquire(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	started := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(started)
		unlock, e := locks.Acquire(context.Background(), "one")
		if e == nil {
			close(acquired)
			unlock()
		}
	}()
	<-started
	other, err := locks.Acquire(context.Background(), "two")
	if err != nil {
		t.Fatalf("independent source was blocked: %v", err)
	}
	other()
	select {
	case <-acquired:
		t.Fatal("same source was acquired twice")
	default:
	}
	releaseFirst()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("same-source waiter did not proceed")
	}
}

func TestCanceledWaiterReleasesItsReference(t *testing.T) {
	locks := New()
	unlock, err := locks.Acquire(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.Acquire(ctx, "one"); err == nil {
		t.Fatal("canceled waiter acquired lock")
	}
	unlock()
	unlockAgain, err := locks.Acquire(context.Background(), "one")
	if err != nil {
		t.Fatalf("lock reference leaked: %v", err)
	}
	unlockAgain()
}
