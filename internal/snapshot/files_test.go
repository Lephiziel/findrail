package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectFileProfileRequiresRegularBoundedInput(t *testing.T) {
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: Origin{ID: "file-profile", Kind: "filesystem", Name: "Synthetic", Location: "/synthetic", IndexedAt: "2026-10-09T11:00:00Z"}}, []Document{{ID: "doc", Path: "note.md", Title: "Note", URI: "file:///synthetic/note.md", MediaType: "text/plain", Text: "fixture", ContentHash: strings.Repeat("a", 64), SizeBytes: 7, ModifiedAt: "2026-10-09T10:00:00Z"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "source.zip")
	if err = os.WriteFile(archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, size, err := InspectFileProfile(context.Background(), archive, MaxArchive, MaxUncompressed, MaxDocuments, MaxPages, true)
	if err != nil || size != int64(len(data)) || len(got.Documents) != 1 {
		t.Fatalf("inspect file: size=%d archive=%+v err=%v", size, got, err)
	}
	if _, _, err = InspectFileProfile(context.Background(), filepath.Join(dir, "missing.zip"), MaxArchive, MaxUncompressed, MaxDocuments, MaxPages, true); err == nil {
		t.Fatal("accepted missing path")
	}
	if _, _, err = InspectFileProfile(context.Background(), dir, MaxArchive, MaxUncompressed, MaxDocuments, MaxPages, true); err == nil {
		t.Fatal("accepted directory")
	}
	if _, _, err = InspectFileProfile(context.Background(), archive, int64(len(data)-1), MaxUncompressed, MaxDocuments, MaxPages, true); err == nil {
		t.Fatal("accepted oversized input")
	}
	if os.PathSeparator != '\\' {
		link := filepath.Join(dir, "link.zip")
		if err = os.Symlink(archive, link); err != nil {
			t.Fatal(err)
		}
		if _, _, err = InspectFileProfile(context.Background(), link, MaxArchive, MaxUncompressed, MaxDocuments, MaxPages, true); err == nil {
			t.Fatal("accepted symlink input")
		}
	}
}
