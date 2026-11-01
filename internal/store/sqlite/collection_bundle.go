package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/evidencebundle"
	"github.com/Lephiziel/findrail/internal/search"
)

const bundleCollectedAtFormat = "2006-01-02T15:04:05.000000000Z07:00"

type BundleEntryStatus struct {
	Index  int    `json:"index"`
	Status string `json:"status"`
}
type BundlePreview struct {
	Entries []BundleEntryStatus `json:"entries"`
	Claim   string              `json:"claim,omitempty"`
	Binding string              `json:"-"`
	Ready   bool                `json:"-"`
}

// PreviewEvidenceBundle checks only the indexed snapshot; it never opens provenance.
func (s *Store) PreviewEvidenceBundle(ctx context.Context, b evidencebundle.Bundle, mappings map[string]string) (BundlePreview, error) {
	out := BundlePreview{Entries: make([]BundleEntryStatus, 0, len(b.Entries))}
	if err := validateBundleMappings(b, mappings); err != nil {
		return out, err
	}
	tx, err := s.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Binding, err = bundleSourceBinding(ctx, tx, b.Sources, mappings)
	if err != nil {
		return out, err
	}
	var collectionCount int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collections").Scan(&collectionCount); err != nil {
		return out, err
	}
	var ownedPayload int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT sum(length(CAST(snapshot_json AS BLOB))) FROM evidence_collection_entries),0)+COALESCE((SELECT sum(length(CAST(title AS BLOB))) FROM evidence_collections),0)").Scan(&ownedPayload); err != nil {
		return out, err
	}
	var ownedMemory int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(length(CAST(snapshot_json AS BLOB))),0) FROM evidence_collection_entries").Scan(&ownedMemory); err != nil {
		return out, err
	}
	for i, entry := range b.Entries {
		source := mappings[entry.SourceAlias]
		status := "match"
		var id, path, media, hash string
		if source == "" {
			status = "unknown_source"
		} else {
			var registered int
			if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM sources WHERE id=?", source).Scan(&registered); e != nil {
				return out, e
			}
			if registered == 0 {
				out.Entries = append(out.Entries, BundleEntryStatus{Index: i, Status: "unknown_source"})
				continue
			}
			err = tx.QueryRowContext(ctx, `SELECT id,path,media_type,content_hash FROM documents WHERE source_id=? AND path=?`, source, entry.RelativePath).Scan(&id, &path, &media, &hash)
			if errors.Is(err, sql.ErrNoRows) {
				status = "missing_document"
			} else if err != nil {
				return out, err
			} else if hash != entry.ContentHash {
				status = "changed_hash"
			} else if media != entry.MediaType {
				status = "quote_mismatch"
			} else {
				pageOK := true
				if entry.Page > 0 {
					var n int
					e := tx.QueryRowContext(ctx, "SELECT count(*) FROM document_pages WHERE document_id=? AND page_number=?", id, entry.Page).Scan(&n)
					pageOK = e == nil && n == 1
				}
				if !pageOK {
					status = "invalid_page"
				} else {
					var text string
					if entry.Page > 0 {
						err = tx.QueryRowContext(ctx, "SELECT substr(content,?,?) FROM document_pages WHERE document_id=? AND page_number=?", entry.StartChar+1, len([]rune(entry.Text)), id, entry.Page).Scan(&text)
					} else {
						err = tx.QueryRowContext(ctx, "SELECT substr(content,?,?) FROM documents WHERE id=?", entry.StartChar+1, len([]rune(entry.Text)), id).Scan(&text)
					}
					if err != nil || text != entry.Text {
						status = "quote_mismatch"
					}
				}
			}
		}
		out.Entries = append(out.Entries, BundleEntryStatus{Index: i, Status: status})
	}
	projectedPayload, projectedMemory, projectedText := 0, 0, 0
	projectedAt := time.Now().UTC().Format(bundleCollectedAtFormat)
	for i, entry := range b.Entries {
		if out.Entries[i].Status != "match" {
			continue
		}
		var docID string
		if err = tx.QueryRowContext(ctx, "SELECT id FROM documents WHERE source_id=? AND path=?", mappings[entry.SourceAlias], entry.RelativePath).Scan(&docID); err != nil {
			return out, err
		}
		ev, e := bundleEvidenceInTx(ctx, tx, docID, entry)
		if e != nil {
			return out, e
		}
		item := CollectionEntry{Key: "00000000000000000000000000000000", Evidence: ev, Excerpt: entry.Text, Note: entry.Note, WholePreview: true, CollectedAt: projectedAt}
		raw, e := json.Marshal(item)
		if e != nil {
			return out, e
		}
		metadata := item
		metadata.Evidence.Text = ""
		metadata.Excerpt = ""
		metadata.Note = ""
		mb, e := json.Marshal(metadata)
		if e != nil {
			return out, e
		}
		projectedPayload += len(raw)
		projectedMemory += len(raw)
		projectedText += len(ev.Text) + len(entry.Text)
		if len(mb) > 16<<10 || len(entry.Note) > maxCollectionNote {
			out.Entries[i].Status = "quota_conflict"
		}
	}
	quotaConflict := collectionCount >= maxCollections || len(b.Entries) > maxCollectionEntries || ownedPayload+projectedPayload+256 > maxCollectionPayload || ownedMemory+projectedMemory > maxCollectionOwnedMemory || projectedText > maxCollectionText
	if quotaConflict {
		for i := range out.Entries {
			if out.Entries[i].Status == "match" {
				out.Entries[i].Status = "quota_conflict"
			}
		}
	}
	out.Ready = !quotaConflict
	for _, status := range out.Entries {
		if status.Status != "match" {
			out.Ready = false
			break
		}
	}
	return out, nil
}

// ImportEvidenceBundle performs final identity checks and all inserts in one writer transaction.
func (s *Store) ImportEvidenceBundle(ctx context.Context, b evidencebundle.Bundle, mappings map[string]string, title, expectedBinding string) (Collection, error) {
	var out Collection
	if s.db == nil {
		return out, errors.New("index is read-only")
	}
	if !collectionTitle(title) {
		return out, ErrCollectionLimit
	}
	if err := validateBundleMappings(b, mappings); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	binding, err := bundleSourceBinding(ctx, tx, b.Sources, mappings)
	if err != nil {
		return out, err
	}
	if binding != expectedBinding {
		return out, ErrCollectionSnapshotChanged
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collections").Scan(&count); err != nil {
		return out, err
	}
	if count >= maxCollections {
		return out, ErrCollectionLimit
	}
	var ownedMemory int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(length(CAST(snapshot_json AS BLOB))),0) FROM evidence_collection_entries").Scan(&ownedMemory); err != nil {
		return out, err
	}
	for _, src := range b.Sources {
		if mappings[src.Alias] == "" {
			return out, errors.New("every source alias must be mapped")
		}
	}
	id, err := newCollectionID()
	if err != nil {
		return out, err
	}
	now := time.Now().UTC().Format(bundleCollectedAtFormat)
	items := make([]CollectionEntry, 0, len(b.Entries))
	textBytes, total := 0, 0
	for _, entry := range b.Entries {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		var docID string
		err = tx.QueryRowContext(ctx, "SELECT id FROM documents WHERE source_id=? AND path=? AND media_type=? AND content_hash=?", mappings[entry.SourceAlias], entry.RelativePath, entry.MediaType, entry.ContentHash).Scan(&docID)
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrCollectionSnapshotChanged
		}
		if err != nil {
			return out, err
		}
		ev, e := bundleEvidenceInTx(ctx, tx, docID, entry)
		if e != nil {
			return out, ErrCollectionSnapshotChanged
		}
		if ev.Path != entry.RelativePath || ev.MediaType != entry.MediaType || ev.ContentHash != entry.ContentHash {
			return out, ErrCollectionSnapshotChanged
		}
		key, e := newCollectionID()
		if e != nil {
			return out, e
		}
		item := CollectionEntry{Key: key, Evidence: ev, Excerpt: entry.Text, Note: entry.Note, WholePreview: true, CollectedAt: now}
		raw, e := json.Marshal(item)
		if e != nil {
			return out, e
		}
		if len(raw) > 4<<20 {
			return out, ErrCollectionLimit
		}
		metadata := item
		metadata.Evidence.Text = ""
		metadata.Excerpt = ""
		metadata.Note = ""
		mb, e := json.Marshal(metadata)
		if e != nil {
			return out, e
		}
		if len(mb) > 16<<10 {
			return out, ErrCollectionLimit
		}
		textBytes += len(ev.Text) + len(entry.Text)
		total += len(raw)
		if textBytes > maxCollectionText || len(entry.Note) > maxCollectionNote || total > maxCollectionPayload || ownedMemory+total > maxCollectionOwnedMemory {
			return out, ErrCollectionLimit
		}
		items = append(items, item)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO evidence_collections VALUES(?,?,1,?,?)", id, title, now, now); err != nil {
		return out, err
	}
	for i, item := range items {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		raw, e := json.Marshal(item)
		if e != nil {
			return out, e
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json,preview_start_char) VALUES(?,?,?,?,?,?,?)", id, item.Key, i, item.Evidence.SourceID, item.Evidence.ID, string(raw), item.Evidence.TextStartChar); err != nil {
			return out, err
		}
	}
	var aggregate int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT sum(length(CAST(snapshot_json AS BLOB))) FROM evidence_collection_entries),0)+COALESCE((SELECT sum(length(CAST(title AS BLOB))) FROM evidence_collections),0)").Scan(&aggregate); err != nil {
		return out, err
	}
	var memory int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(length(CAST(snapshot_json AS BLOB))),0) FROM evidence_collection_entries").Scan(&memory); err != nil {
		return out, err
	}
	if aggregate > maxCollectionPayload || memory > maxCollectionOwnedMemory {
		return out, ErrCollectionLimit
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	// Return precisely the snapshot that committed. A subsequent pooled read
	// could time out or observe a concurrent edit after the import succeeded.
	return Collection{ID: id, Title: title, Revision: 1, CreatedAt: now, UpdatedAt: now, EntryCount: len(items), Entries: items}, nil
}

func validateBundleMappings(b evidencebundle.Bundle, mappings map[string]string) error {
	if _, err := evidencebundle.Encode(b); err != nil {
		return err
	}
	if len(mappings) != len(b.Sources) {
		return evidencebundle.ErrInvalid
	}
	for _, source := range b.Sources {
		id := mappings[source.Alias]
		if id == "" || len(id) > 512 || !utf8.ValidString(id) {
			return evidencebundle.ErrInvalid
		}
	}
	return nil
}

func bundleEvidenceInTx(ctx context.Context, tx *sql.Tx, docID string, entry evidencebundle.Entry) (search.Evidence, error) {
	loadStart := entry.StartChar
	if entry.Page > 0 {
		loadStart = 0
	}
	ev, err := evidenceInWriteTxAt(ctx, tx, docID, entry.Page, loadStart)
	if err != nil {
		return ev, err
	}
	var exact string
	if entry.Page > 0 {
		err = tx.QueryRowContext(ctx, "SELECT length(content),substr(content,?,?) FROM document_pages WHERE document_id=? AND page_number=?", entry.StartChar+1, len([]rune(entry.Text)), docID, entry.Page).Scan(&ev.TextTotalChars, &exact)
	} else {
		err = tx.QueryRowContext(ctx, "SELECT length(content),substr(content,?,?) FROM documents WHERE id=?", entry.StartChar+1, len([]rune(entry.Text)), docID).Scan(&ev.TextTotalChars, &exact)
	}
	if err != nil || exact != entry.Text || entry.StartChar+utf8.RuneCountInString(entry.Text) > ev.TextTotalChars {
		return ev, ErrCollectionSnapshotChanged
	}
	ev.Text = entry.Text
	ev.TextStartChar = entry.StartChar
	ev.Truncated = entry.StartChar > 0 || entry.StartChar+utf8.RuneCountInString(entry.Text) < ev.TextTotalChars
	return ev, nil
}

func bundleSourceBinding(ctx context.Context, tx *sql.Tx, sources []evidencebundle.Source, mappings map[string]string) (string, error) {
	h := sha256.New()
	for _, source := range sources {
		id := mappings[source.Alias]
		if id == "" {
			return "", errors.New("every source alias must be mapped")
		}
		var registered string
		var revision int64
		if err := tx.QueryRowContext(ctx, "SELECT registration_token,revision FROM sources WHERE id=?", id).Scan(&registered, &revision); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			registered = "missing"
		}
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00", source.Alias, id, revision)
		h.Write([]byte(registered))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
