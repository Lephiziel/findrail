// Command evidence_collection_fixture seeds an offline synthetic GitHub source
// for the compiled browser smoke. It performs no network calls.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: evidence_collection_fixture INDEX_DIR")
	}
	ctx := context.Background()
	dir, err := filepath.Abs(os.Args[1])
	if err != nil {
		panic(err)
	}
	commit := "0123456789abcdef0123456789abcdef01234567"
	sourceHash := sha256.Sum256([]byte("https://github.com/example/evidence\x00" + commit))
	sourceID := fmt.Sprintf("gh_%x", sourceHash)
	path := "docs/<img onerror=alert(2)>.md"
	content := "Synthetic pinned Github evidence marker."
	docHash := sha256.Sum256([]byte(content))
	docIDHash := sha256.Sum256([]byte(sourceID + "\x00" + path))
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		panic(err)
	}
	scan, err := store.BeginScan(ctx, connector.Source{ID: sourceID, Kind: "github", Name: "Synthetic pinned GitHub", Root: "https://github.com/example/evidence", MaxTextBytes: 1 << 20})
	if err != nil {
		_ = store.Close()
		panic(err)
	}
	if _, err = scan.Upsert(ctx, connector.Document{ID: fmt.Sprintf("doc_%x", docIDHash), SourceID: sourceID, Title: "<img onerror=alert(2)>.md", URI: "https://github.com/example/evidence/blob/" + commit + "/docs/%3Cimg%20onerror%3Dalert(2)%3E.md", Path: path, Content: content, Hash: fmt.Sprintf("%x", docHash), SizeBytes: int64(len(content)), ModifiedAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), MediaType: "text/markdown"}); err != nil {
		_ = scan.Rollback()
		_ = store.Close()
		panic(err)
	}
	if _, err = scan.Commit(ctx); err != nil {
		_ = store.Close()
		panic(err)
	}
	if err = store.Close(); err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `INSERT INTO github_sources(source_id,repository_id,owner,repo,repository_url,ref_mode,ref_value,selected_path,max_bytes,policy_version,snapshot_sha,commit_time,registration_token,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sourceID, 1, "example", "evidence", "https://github.com/example/evidence", "commit", commit, "", 1<<20, 1, commit, "2026-10-11T00:00:00Z", "synthetic-fixture-token", 1)
	if err != nil {
		panic(err)
	}
	fmt.Print(sourceID)
}
