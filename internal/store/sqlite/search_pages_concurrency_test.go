package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
	modernsqlite "modernc.org/sqlite"
)

// The real SQLite driver is wrapped only to pause a chosen result read. No
// production hook is needed and transaction/fence behavior still comes from WAL.
type pageReadBarrier struct {
	query         string
	occurrence    int32
	seen          atomic.Int32
	entered, done chan struct{}
	releaseOnce   sync.Once
}

func newPageReadBarrier(query string, occurrence int32) *pageReadBarrier {
	return &pageReadBarrier{query: query, occurrence: occurrence, entered: make(chan struct{}), done: make(chan struct{})}
}
func (b *pageReadBarrier) release() { b.releaseOnce.Do(func() { close(b.done) }) }
func (b *pageReadBarrier) wait(ctx context.Context) error {
	close(b.entered)
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type pageBarrierConnector struct {
	dsn     string
	barrier *pageReadBarrier
	driver  modernsqlite.Driver
}

func (c *pageBarrierConnector) Driver() driver.Driver { return &c.driver }
func (c *pageBarrierConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &pageBarrierConn{Conn: conn, barrier: c.barrier}, nil
}

type pageBarrierConn struct {
	driver.Conn
	barrier *pageReadBarrier
}

func (c *pageBarrierConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *pageBarrierConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *pageBarrierConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err == nil && strings.Contains(q, c.barrier.query) && c.barrier.seen.Add(1) == c.barrier.occurrence {
		return &pageBarrierRows{Rows: rows, ctx: ctx, barrier: c.barrier}, nil
	}
	return rows, err
}
func (c *pageBarrierConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *pageBarrierConn) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }

type pageBarrierRows struct {
	driver.Rows
	ctx     context.Context
	barrier *pageReadBarrier
	paused  bool
}

func (r *pageBarrierRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil && !r.paused {
		r.paused = true
		// Reading before the barrier establishes the SQLite snapshot (or reads
		// the fence), then the test can commit through another connection.
		return r.barrier.wait(r.ctx)
	}
	return err
}

func installPageBarrier(t *testing.T, s *Store, dir string, barrier *pageReadBarrier) {
	t.Helper()
	if err := s.readers.Close(); err != nil {
		t.Fatal(err)
	}
	u := sqliteFileURL(filepath.Join(dir, "findrail.db"))
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	s.readers = sql.OpenDB(&pageBarrierConnector{dsn: u.String(), barrier: barrier})
	s.readers.SetMaxOpenConns(4)
	t.Cleanup(barrier.release)
}

func seedConcurrentPages(t *testing.T, s *Store, marker string) {
	t.Helper()
	scan, err := s.BeginScan(t.Context(), connector.Source{ID: "concurrent-pages", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("page-%d", i)
		if _, err := scan.Upsert(t.Context(), connector.Document{ID: id, SourceID: "concurrent-pages", Title: id, Path: "docs/" + id, URI: "file:///synthetic/" + id, Content: "controlledmarker " + marker, Hash: marker + id, MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func waitPageBarrier(t *testing.T, b *pageReadBarrier) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("SQLite page did not reach the read barrier")
	}
}

func TestSearchPageRejectsExternalCommitDuringRead(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedConcurrentPages(t, s, "old")
	external, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	barrier := newPageReadBarrier("SELECT COUNT(*) FROM documents_fts", 1)
	installPageBarrier(t, s, dir, barrier)
	defer barrier.release()
	done := make(chan error, 1)
	go func() {
		page, e := s.SearchPage(t.Context(), search.PageRequest{Request: search.Request{Query: "controlledmarker", Limit: 1}})
		if e == nil || len(page.Results) != 0 {
			e = fmt.Errorf("mixed page was published: %+v, %v", page, e)
		}
		done <- e
	}()
	waitPageBarrier(t, barrier)
	seedConcurrentPages(t, external, "new")
	barrier.release()
	if err := <-done; !errors.Is(err, search.ErrCursorStale) {
		t.Fatalf("writer-during-read error: %v", err)
	}
	page, err := s.SearchPage(t.Context(), search.PageRequest{Request: search.Request{Query: "new", Limit: 1}})
	if err != nil || page.Total != 4 || page.NextCursor == nil {
		t.Fatalf("fresh search after stale read: %+v, %v", page, err)
	}
}

func TestSearchPageCancelledFenceReadKeepsSession(t *testing.T) {
	for _, occurrence := range []int32{3, 4} {
		t.Run(fmt.Sprint(occurrence), func(t *testing.T) {
			cancelledFenceReadKeepsSession(t, occurrence)
		})
	}
}

func cancelledFenceReadKeepsSession(t *testing.T, occurrence int32) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedConcurrentPages(t, s, "old")
	// Pause either fence read of the second request, without changing the
	// real driver connection. Cancellation must not manufacture a new epoch.
	barrier := newPageReadBarrier("PRAGMA data_version", occurrence)
	installPageBarrier(t, s, dir, barrier)
	defer barrier.release()
	request := search.PageRequest{Request: search.Request{Query: "controlledmarker", Limit: 1}}
	first, err := s.SearchPage(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := s.SearchPage(ctx, request); done <- e }()
	waitPageBarrier(t, barrier)
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatalf("cancelled fence read: %v", e)
	}
	request.Cursor = *first.NextCursor
	page, err := s.SearchPage(t.Context(), request)
	if err != nil || page.Offset != 1 {
		t.Fatalf("cancellation changed the valid cursor session: %+v, %v", page, err)
	}
}

func TestSearchPageCloseCancelsSQLAndPreservesOtherReaders(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedConcurrentPages(t, s, "old")
	barrier := newPageReadBarrier("SELECT COUNT(*) FROM documents_fts", 1)
	installPageBarrier(t, s, dir, barrier)
	defer barrier.release()
	request := search.PageRequest{Request: search.Request{Query: "controlledmarker", Limit: 1}}
	done := make(chan error, 1)
	go func() { _, e := s.SearchPage(t.Context(), request); done <- e }()
	waitPageBarrier(t, barrier)
	if ordinary, e := s.Search(t.Context(), request.Request); e != nil || ordinary.Total != 4 {
		t.Fatalf("pager blocked ordinary Search: %+v, %v", ordinary, e)
	}
	if evidence, e := s.Evidence(t.Context(), "page-0", 0); e != nil || evidence.ID != "page-0" {
		t.Fatalf("pager blocked Evidence: %+v, %v", evidence, e)
	}
	held := []*sql.Conn{}
	for i := 0; i < 3; i++ {
		conn, e := s.readers.Conn(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		held = append(held, conn)
	}
	if stats := s.readers.Stats(); stats.MaxOpenConnections != 4 || stats.OpenConnections != 4 || stats.InUse != 4 {
		t.Errorf("pinned reader exceeded the shared budget: %+v", stats)
	}
	for _, conn := range held {
		_ = conn.Close()
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case e := <-done:
		if !errors.Is(e, search.ErrPageClosed) {
			t.Errorf("close during SQL: %v", e)
		}
	case <-time.After(2 * time.Second):
		barrier.release()
		<-closed
		t.Fatal("Close did not cancel an active SQL read")
	}
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if s.readers.Stats().OpenConnections != 0 {
		t.Fatal("pinned reader survived SQL cancellation/Close")
	}
}
