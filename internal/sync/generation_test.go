package sync

import (
	"context"
	"testing"
	"time"
)

func TestOldGenerationCannotResurrectStatusOrPublishProgress(t *testing.T) {
	m := New(nil, Config{})
	ctx := context.Background()
	m.mu.Lock()
	m.generations["source"] = 2
	m.status["source"] = Status{SourceID: "source", State: "starting", Progress: Progress{AttemptID: "new-attempt"}}
	m.mu.Unlock()

	m.update(ctx, "source", 1, func(s *Status) { s.State = "error"; s.LastError = "stale" })
	m.progress(ctx, "source", 1, "old-attempt", time.Now(), 9, 0, true, "done", true, "old-report", "")
	m.update(ctx, "forgotten", 1, func(s *Status) { s.State = "idle" })

	got := m.Status()
	if len(got) != 1 || got[0].State != "starting" || got[0].Progress.AttemptID != "new-attempt" || got[0].Progress.ReportID != "" {
		t.Fatalf("stale worker changed current status: %+v", got)
	}
}

func TestCanceledGenerationCannotUpdate(t *testing.T) {
	m := New(nil, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.generations["source"] = 1
	m.mu.Unlock()
	cancel()
	m.update(ctx, "source", 1, func(s *Status) { s.State = "idle" })
	if got := m.Status(); len(got) != 0 {
		t.Fatalf("canceled worker published status: %+v", got)
	}
}
