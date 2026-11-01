package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/originals"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func passageDocument(t *testing.T, s *Store, title, body string) search.Evidence {
	t.Helper()
	ctx := context.Background()
	scan, err := s.BeginScan(ctx, connector.Source{ID: "passage-source", Name: "Synthetic notes", Kind: "filesystem"})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	_, err = scan.Upsert(ctx, connector.Document{ID: "passage-doc", SourceID: "passage-source", Title: title, Path: "note.md", URI: "file:///synthetic/note.md", MediaType: "text/plain", Content: body, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(body)))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Evidence(ctx, "passage-doc", 0)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestPassageWindowsBoundariesAndCanonicalSavedTail(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := strings.Repeat("padding ", 10000) + "😀e\u0301 needlelatebody tail"
	ev := passageDocument(t, s, "note", body)
	anchored, err := s.Passage(ctx, ev.ID, "needlelatebody", ev.OriginalIdentity, 0)
	if err != nil {
		t.Fatal(err)
	}
	if anchored.TextStartChar != 80004 || !anchored.Truncated || anchored.TextTotalChars != utf8.RuneCountInString(body) || anchored.Text != "needlelatebody tail" {
		t.Fatalf("tail must remain a partial document: %+v", anchored)
	}
	for _, start := range []int{0, 65535, 65536, 80000, utf8.RuneCountInString(body)} {
		window, err := s.Passage(ctx, ev.ID, "", ev.OriginalIdentity, start)
		if err != nil {
			t.Fatal(err)
		}
		want := []rune(body)[start:]
		if len(want) > 65536 {
			want = want[:65536]
		}
		if window.Text != string(want) || !utf8.ValidString(window.Text) || len(window.Text) > 256<<10 || window.Truncated != (start > 0 || len(want) < utf8.RuneCountInString(body)) {
			t.Fatalf("Unicode window at %d is incorrect: %+v", start, window)
		}
	}
	c, err := s.CreateCollection(ctx, "Tail")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.SaveCollection(ctx, c.ID, c.Revision, c.Title, []CollectionEdit{{DocumentID: ev.ID, Excerpt: anchored.Text, WholePreview: true, OriginalIdentity: ev.OriginalIdentity, TextStartChar: anchored.TextStartChar, Note: "late note"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Entries[0]; !got.Evidence.Truncated || got.Evidence.Text != anchored.Text || got.Evidence.TextStartChar != anchored.TextStartChar || got.Note != "late note" {
		t.Fatalf("canonical persisted tail: %+v", got)
	}
	if _, err := s.Passage(ctx, ev.ID, "", ev.OriginalIdentity, utf8.RuneCountInString(body)+1); err != search.ErrPage {
		t.Fatalf("out-of-range offset: %v", err)
	}
	newer := passageDocument(t, s, "note", body+" updated")
	if newer.OriginalIdentity == ev.OriginalIdentity {
		t.Fatal("fixture identity did not change")
	}
	if _, err := s.Passage(ctx, ev.ID, "needlelatebody", ev.OriginalIdentity, 0); err != originals.ErrSnapshotChanged {
		t.Fatalf("stale identity accepted: %v", err)
	}
}

func TestPassageFTSAnchorsAndWorkCap(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct{ title, body, query, kind string }{
		{"needle title", "unrelated text", "needle", "title_only"},
		{"note", strings.Repeat("padding ", 9000) + "CAFÉ résumé", "cafe resume", "literal_body"},
		{"note", "\x01findrail-open\x02" + strings.Repeat("x", 30000) + " needle", "needle", "literal_body"},
		{"note", strings.Repeat("padding ", 140000) + " needle", "needle", "anchor_unavailable"},
	} {
		ev := passageDocument(t, s, tc.title, tc.body)
		got, err := s.Passage(ctx, ev.ID, tc.query, ev.OriginalIdentity, 0)
		if err != nil || got.MatchKind != tc.kind || len(got.Text) > 256<<10 || utf8.RuneCountInString(got.Text) > 65536 {
			t.Fatalf("kind=%s got=%+v err=%v", tc.kind, got, err)
		}
		if tc.kind == "anchor_unavailable" {
			late, err := s.Passage(ctx, ev.ID, "", ev.OriginalIdentity, len([]rune(tc.body))-7)
			if err != nil || !strings.Contains(late.Text, "needle") {
				t.Fatalf("manual window unavailable after cap: %+v %v", late, err)
			}
		}
	}
}
