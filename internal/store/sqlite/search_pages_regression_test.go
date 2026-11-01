package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
)

func TestSearchPageEmptyHasCompleteReason(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.SearchPage(t.Context(), search.PageRequest{Request: search.Request{Query: "absent"}})
	if err != nil || page.Total != 0 || page.Results == nil || page.Returned != 0 || page.NextCursor != nil || page.EndReason == nil || *page.EndReason != "complete" {
		t.Fatalf("empty page did not complete: %+v, %v", page, err)
	}
}

func TestSearchPageValidatesPathBeforeCanonicalization(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, path := range []string{"/", "docs//"} {
		_, err := s.SearchPage(t.Context(), search.PageRequest{Request: search.Request{Query: "needle", PathPrefix: path}})
		var validation *search.ValidationError
		if !errors.As(err, &validation) || validation.Code != "invalid_filter" {
			t.Errorf("invalid path %q accepted or misclassified: %v", path, err)
		}
	}
}

func TestSearchPageCloseIsIdempotent(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SearchPage(t.Context(), search.PageRequest{Request: search.Request{Query: "needle"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("repeated close failed: %v", err)
	}
}

// Holding the real reader pool makes lazy connection acquisition observable
// without sleeps or changing production synchronization.
func holdPageReaders(t *testing.T, s *Store) func() {
	t.Helper()
	connections := []*sql.Conn{}
	for i := 0; i < 4; i++ {
		conn, err := s.readers.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, conn := range connections {
				_ = conn.Close()
			}
		})
	}
}

func waitPageCondition(t *testing.T, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("pager did not reach the controlled synchronization point")
		}
	}
}

func TestSearchPageConcurrentInitHonorsShorterDeadline(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release := holdPageReaders(t, s)
	defer release()
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() {
		_, e := s.SearchPage(firstCtx, search.PageRequest{Request: search.Request{Query: "needle"}})
		first <- e
	}()
	waitPageCondition(t, func() bool { return s.readers.Stats().WaitCount > 0 })
	shortCtx, cancelShort := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelShort()
	second := make(chan error, 1)
	go func() {
		_, e := s.SearchPage(shortCtx, search.PageRequest{Request: search.Request{Query: "needle"}})
		second <- e
	}()
	select {
	case err := <-second:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("shorter deadline error: %v", err)
		}
	case <-time.After(time.Second):
		cancelFirst()
		<-second
		t.Error("short caller deadline was blocked behind connection initialization")
	}
	cancelFirst()
	<-first
}

func TestSearchPageAdmissionAndCloseDuringInitialization(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release := holdPageReaders(t, s)
	defer release()
	request := search.PageRequest{Request: search.Request{Query: "needle"}}
	results := make(chan error, 9)
	go func() { _, e := s.SearchPage(t.Context(), request); results <- e }()
	waitPageCondition(t, func() bool { return s.readers.Stats().WaitCount > 0 })
	s.pagerMu.Lock()
	p := s.pager
	s.pagerMu.Unlock()
	if p == nil {
		t.Fatal("pager did not publish its cancellable initialization state")
	}
	queueCtx, cancelQueue := context.WithCancel(t.Context())
	defer cancelQueue()
	queued := make(chan error, 1)
	go func() { _, e := s.SearchPage(queueCtx, request); queued <- e }()
	for i := 0; i < 7; i++ {
		go func() { _, e := s.SearchPage(t.Context(), request); results <- e }()
	}
	waitPageCondition(t, func() bool { return len(p.gate) == 9 })
	if _, e := s.SearchPage(t.Context(), request); !errors.Is(e, search.ErrPageBusy) {
		t.Errorf("overflow did not fail immediately: %v", e)
	}
	cancelQueue()
	if e := <-queued; !errors.Is(e, context.Canceled) {
		t.Errorf("queued cancellation: %v", e)
	}
	waitPageCondition(t, func() bool { return len(p.gate) == 8 })
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	for i := 0; i < 8; i++ {
		select {
		case e := <-results:
			if !errors.Is(e, search.ErrPageClosed) {
				t.Errorf("closing page operation: %v", e)
			}
		case <-time.After(time.Second):
			release()
			<-closed
			t.Fatal("Close did not cancel connection acquisition and queued calls")
		}
	}
	// Ordinary readers can now return; pager work was already cancelled while
	// every connection was still held by those independent readers.
	release()
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if len(p.gate) != 0 || len(p.serial) != 0 || s.readers.Stats().OpenConnections != 0 {
		t.Fatal("pager leaked admitted work or reader connections")
	}
}

func TestSearchPageConcurrentFirstCallsAndRepeatedClose(t *testing.T) {
	for i := 0; i < 12; i++ {
		s, err := Open(t.Context(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 18)
		for j := 0; j < 16; j++ {
			go func() {
				<-start
				_, e := s.SearchPage(t.Context(), search.PageRequest{Request: search.Request{Query: "needle"}})
				results <- e
			}()
		}
		for j := 0; j < 2; j++ {
			go func() { <-start; results <- s.Close() }()
		}
		close(start)
		for j := 0; j < 18; j++ {
			select {
			case e := <-results:
				if e != nil && !errors.Is(e, search.ErrPageClosed) && !errors.Is(e, search.ErrPageBusy) {
					t.Errorf("concurrent initialization/Close: %v", e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("concurrent initialization/Close hung")
			}
		}
		if s.readers.Stats().OpenConnections != 0 {
			t.Fatal("read connection survived Close")
		}
	}
}
