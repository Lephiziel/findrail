package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/search"
)

func TestMCPPassageScopeAndReRegistration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := strings.Repeat("padding 😀 ", 8000) + "lateanchor end"
	e := passageDocument(t, s, "note", body)
	for _, source := range []string{"", "different-source"} {
		if _, err := s.PassageForSource(ctx, source, e.ID, "lateanchor", e.OriginalIdentity, 0); !errors.Is(err, search.ErrNotFound) {
			t.Fatalf("wrong scope: %v", err)
		}
	}
	got, err := s.PassageForSource(ctx, e.SourceID, e.ID, "lateanchor", e.OriginalIdentity, 0)
	if err != nil || got.Text != "lateanchor end" || got.TextStartChar <= 65536 {
		t.Fatalf("late scoped passage: %v %+v", err, got)
	}
	if err := s.ForgetSource(ctx, e.SourceID); err != nil {
		t.Fatal(err)
	}
	newer := passageDocument(t, s, "note", body)
	if newer.OriginalIdentity == e.OriginalIdentity {
		t.Fatal("re-registration retained identity")
	}
	if _, err := s.PassageForSource(ctx, e.SourceID, e.ID, "lateanchor", e.OriginalIdentity, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("old registration accepted: %v", err)
	}
	if _, err := s.PassageForSource(ctx, newer.SourceID, newer.ID, "lateanchor", newer.OriginalIdentity, 0); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.PassageForSource(canceled, newer.SourceID, newer.ID, "lateanchor", newer.OriginalIdentity, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
}
