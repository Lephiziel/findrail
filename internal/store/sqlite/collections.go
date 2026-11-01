package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/evidencebundle"
	"github.com/Lephiziel/findrail/internal/originals"
	"github.com/Lephiziel/findrail/internal/search"
)

const (
	maxCollections           = 100
	maxCollectionEntries     = 40
	maxCollectionText        = 2 << 20
	maxCollectionPayload     = 64 << 20
	maxCollectionOwnedMemory = 32 << 20
	maxCollectionNote        = 2048
)

var ErrCollectionNotFound = errors.New("collection not found")
var ErrCollectionConflict = errors.New("collection revision conflict")
var ErrCollectionLimit = errors.New("collection limit exceeded")
var ErrCollectionSnapshotChanged = errors.New("indexed evidence changed; reload its preview before saving")

type CollectionEntry struct {
	Key          string          `json:"key"`
	Evidence     search.Evidence `json:"evidence"`
	Excerpt      string          `json:"excerpt"`
	WholePreview bool            `json:"whole_preview"`
	Note         string          `json:"note"`
	CollectedAt  string          `json:"collected_at"`
}

// ExportEvidenceBundle returns the portable citations from one collection read snapshot.
func (s *Store) ExportEvidenceBundle(ctx context.Context, id string) (evidencebundle.Bundle, error) {
	c, err := s.LoadCollection(ctx, id)
	if err != nil {
		return evidencebundle.Bundle{}, err
	}
	b := evidencebundle.Bundle{Title: c.Title, Sources: []evidencebundle.Source{}, Entries: []evidencebundle.Entry{}}
	aliases := map[string]string{}
	for _, item := range c.Entries {
		ev := item.Evidence
		alias, ok := aliases[ev.SourceID]
		if !ok {
			if len(aliases) == evidencebundle.MaxSources {
				return evidencebundle.Bundle{}, ErrCollectionLimit
			}
			alias = fmt.Sprintf("s%d", len(aliases)+1)
			aliases[ev.SourceID] = alias
			b.Sources = append(b.Sources, evidencebundle.Source{Alias: alias, Kind: ev.SourceKind, DisplayName: ev.SourceName})
		}
		if ev.Path == "" || ev.ContentHash == "" || !utf8.ValidString(ev.Path) {
			return evidencebundle.Bundle{}, errors.New("collection evidence has no portable locator")
		}
		start := 0
		if item.WholePreview {
			item.Excerpt = ev.Text
			start = ev.TextStartChar
		} else {
			at := strings.Index(ev.Text, item.Excerpt)
			if at < 0 {
				return evidencebundle.Bundle{}, errors.New("collection excerpt has no valid offset")
			}
			start = ev.TextStartChar + utf8.RuneCountInString(ev.Text[:at])
		}
		b.Entries = append(b.Entries, evidencebundle.Entry{SourceAlias: alias, RelativePath: ev.Path, MediaType: ev.MediaType, ContentHash: ev.ContentHash, Page: ev.Page, StartChar: start, Text: item.Excerpt, Note: item.Note})
	}
	if _, err = evidencebundle.Encode(b); err != nil {
		return evidencebundle.Bundle{}, err
	}
	return b, nil
}

type CollectionEdit struct {
	Key              string `json:"key"`
	Note             string `json:"note"`
	DocumentID       string `json:"document_id"`
	Page             int    `json:"page"`
	Excerpt          string `json:"excerpt"`
	WholePreview     bool   `json:"whole_preview"`
	OriginalIdentity string `json:"original_identity"`
	TextStartChar    int    `json:"text_start_char,omitempty"`
}
type Collection struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Revision   int64             `json:"revision"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
	EntryCount int               `json:"entry_count"`
	Entries    []CollectionEntry `json:"entries"`
}

func newCollectionID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func collectionTitle(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(s) <= 256
}
func (s *Store) ListCollections(ctx context.Context) ([]Collection, error) {
	r, e := s.readers.QueryContext(ctx, `SELECT c.id,c.title,c.revision,c.created_at,c.updated_at,COUNT(x.entry_id) FROM evidence_collections c LEFT JOIN evidence_collection_entries x ON x.collection_id=c.id GROUP BY c.id ORDER BY c.updated_at DESC,c.id`)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []Collection{}
	for r.Next() {
		var c Collection
		var n int
		if e = r.Scan(&c.ID, &c.Title, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &n); e != nil {
			return nil, e
		}
		c.Entries = make([]CollectionEntry, 0, n)
		c.EntryCount = n
		out = append(out, c)
	}
	return out, r.Err()
}
func (s *Store) LoadCollection(ctx context.Context, id string) (Collection, error) {
	var c Collection
	tx, e := s.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, "SELECT id,title,revision,created_at,updated_at FROM evidence_collections WHERE id=?", id).Scan(&c.ID, &c.Title, &c.Revision, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return c, ErrCollectionNotFound
	}
	if e != nil {
		return c, e
	}
	c.Entries = []CollectionEntry{}
	r, e := tx.QueryContext(ctx, "SELECT snapshot_json FROM evidence_collection_entries WHERE collection_id=? ORDER BY ordinal", id)
	if e != nil {
		return c, e
	}
	defer r.Close()
	textBytes, payloadBytes := 0, 0
	for r.Next() {
		var b string
		var x CollectionEntry
		if e = r.Scan(&b); e != nil {
			return c, e
		}
		payloadBytes += len(b)
		if len(c.Entries) >= maxCollectionEntries || len(b) > 4<<20 || payloadBytes > 16<<20 {
			return c, ErrCollectionLimit
		}
		if e = json.Unmarshal([]byte(b), &x); e != nil {
			return c, e
		}
		textBytes += len(x.Evidence.Text) + len(x.Excerpt)
		if len(x.Evidence.Text) > 256<<10 || len(x.Note) > maxCollectionNote || textBytes > maxCollectionText {
			return c, ErrCollectionLimit
		}
		c.Entries = append(c.Entries, x)
	}
	if e = r.Err(); e != nil {
		return c, e
	}
	c.EntryCount = len(c.Entries)
	if e = r.Close(); e != nil {
		return c, e
	}
	return c, tx.Commit()
}
func (s *Store) CreateCollection(ctx context.Context, title string) (Collection, error) {
	var c Collection
	if s.db == nil {
		return c, errors.New("index is read-only")
	}
	if !collectionTitle(title) {
		return c, errors.New("title must be 1–256 UTF-8 bytes")
	}
	id, e := newCollectionID()
	if e != nil {
		return c, e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collections").Scan(&count); e != nil {
		return c, e
	}
	if count >= maxCollections {
		return c, ErrCollectionLimit
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO evidence_collections VALUES(?,?,1,?,?)", id, title, now, now); e != nil {
		return c, e
	}
	var payload int
	if e = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT sum(length(CAST(snapshot_json AS BLOB))) FROM evidence_collection_entries),0)+COALESCE((SELECT sum(length(CAST(title AS BLOB))) FROM evidence_collections),0)").Scan(&payload); e != nil {
		return c, e
	}
	if payload > maxCollectionPayload {
		return c, ErrCollectionLimit
	}
	if e = tx.Commit(); e != nil {
		return c, e
	}
	return Collection{ID: id, Title: title, Revision: 1, CreatedAt: now, UpdatedAt: now, Entries: []CollectionEntry{}}, nil
}

// PinCollectionEntry accepts only a document ID, page and selected passage. All provenance is read from the indexed database inside the same writer transaction.
func (s *Store) PinCollectionEntry(ctx context.Context, id string, rev int64, docID string, page int, excerpt string, whole bool, expected string) (Collection, error) {
	return s.PinCollectionEntryAt(ctx, id, rev, docID, page, excerpt, whole, expected, 0)
}

func (s *Store) PinCollectionEntryAt(ctx context.Context, id string, rev int64, docID string, page int, excerpt string, whole bool, expected string, start int) (Collection, error) {
	var out Collection
	if s.db == nil {
		return out, errors.New("index is read-only")
	}
	if strings.TrimSpace(excerpt) == "" || len(excerpt) > 256<<10 || !utf8.ValidString(excerpt) || page < 0 {
		return out, errors.New("invalid passage")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var title string
	var current int64
	if e = tx.QueryRowContext(ctx, "SELECT title,revision FROM evidence_collections WHERE id=?", id).Scan(&title, &current); errors.Is(e, sql.ErrNoRows) {
		return out, ErrCollectionNotFound
	} else if e != nil {
		return out, e
	}
	if current != rev {
		return out, ErrCollectionConflict
	}
	v, e := evidenceInWriteTxAt(ctx, tx, docID, page, start)
	if e != nil {
		return out, e
	}
	if expected == "" || expected != v.OriginalIdentity {
		return out, ErrCollectionSnapshotChanged
	}
	if !strings.Contains(v.Text, excerpt) {
		return out, errors.New("passage is not part of indexed preview")
	}
	if whole && excerpt != v.Text {
		return out, errors.New("whole-preview selection must equal the visible preview")
	}
	check, e := tx.QueryContext(ctx, "SELECT snapshot_json FROM evidence_collection_entries WHERE collection_id=?", id)
	if e != nil {
		return out, e
	}
	retainedText := 0
	for check.Next() {
		var raw string
		var prior CollectionEntry
		if e = check.Scan(&raw); e != nil {
			check.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(raw), &prior); e != nil {
			check.Close()
			return out, e
		}
		retainedText += len(prior.Evidence.Text) + len(prior.Excerpt)
		if prior.Evidence.ID == v.ID && prior.Evidence.SourceID == v.SourceID && prior.Evidence.ContentHash == v.ContentHash && prior.Evidence.Page == v.Page && prior.Excerpt == excerpt {
			check.Close()
			return out, errors.New("duplicate evidence passage")
		}
	}
	if e = check.Err(); e != nil {
		check.Close()
		return out, e
	}
	check.Close()
	var n int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collection_entries WHERE collection_id=?", id).Scan(&n); e != nil {
		return out, e
	}
	if n >= maxCollectionEntries {
		return out, ErrCollectionLimit
	}
	if retainedText+len(v.Text)+len(excerpt) > maxCollectionText {
		return out, ErrCollectionLimit
	}
	key, e := newCollectionID()
	if e != nil {
		return out, e
	}
	entry := CollectionEntry{Key: key, Evidence: v, Excerpt: excerpt, WholePreview: whole, CollectedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, e := json.Marshal(entry)
	if e != nil {
		return out, e
	}
	if len(raw) > 4<<20 {
		return out, ErrCollectionLimit
	}
	metadata := entry
	metadata.Evidence.Text = ""
	metadata.Excerpt = ""
	metadata.Note = ""
	mb, e := json.Marshal(metadata)
	if e != nil {
		return out, e
	}
	if len(mb) > 16<<10 || len(v.Text) > 256<<10 || len(excerpt) > 256<<10 {
		return out, ErrCollectionLimit
	}
	var payload int
	if e = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT sum(length(CAST(snapshot_json AS BLOB))) FROM evidence_collection_entries),0)+COALESCE((SELECT sum(length(CAST(title AS BLOB))) FROM evidence_collections),0)").Scan(&payload); e != nil {
		return out, e
	}
	if payload+len(raw) > maxCollectionPayload {
		return out, ErrCollectionLimit
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, e = tx.ExecContext(ctx, "UPDATE evidence_collections SET revision=revision+1,updated_at=? WHERE id=? AND revision=?", now, id, rev); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json,preview_start_char) VALUES(?,?,?,?,?,?,?)", id, key, n, v.SourceID, v.ID, string(raw), start); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return s.LoadCollection(ctx, id)
}
func (s *Store) UpdateCollection(ctx context.Context, id string, rev int64, title string, entries []CollectionEntry) (Collection, error) {
	var out Collection
	if s.db == nil {
		return out, errors.New("index is read-only")
	}
	if rev < 1 || !collectionTitle(title) || len(entries) > maxCollectionEntries {
		return out, ErrCollectionLimit
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var current int64
	e = tx.QueryRowContext(ctx, "SELECT revision FROM evidence_collections WHERE id=?", id).Scan(&current)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrCollectionNotFound
	}
	if e != nil {
		return out, e
	}
	if current != rev {
		return out, ErrCollectionConflict
	}
	old := map[string]CollectionEntry{}
	r, e := tx.QueryContext(ctx, "SELECT snapshot_json FROM evidence_collection_entries WHERE collection_id=?", id)
	if e != nil {
		return out, e
	}
	for r.Next() {
		var b string
		var x CollectionEntry
		if e = r.Scan(&b); e != nil {
			r.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(b), &x); e != nil {
			r.Close()
			return out, e
		}
		old[x.Key] = x
	}
	if e = r.Err(); e != nil {
		r.Close()
		return out, e
	}
	r.Close()
	seen := map[string]bool{}
	text, total := 0, 0
	raws := make([]string, len(entries))
	for i, x := range entries {
		prior, ok := old[x.Key]
		if !ok || seen[x.Key] || len(x.Note) > maxCollectionNote || !utf8.ValidString(x.Note) {
			return out, errors.New("invalid collection edit")
		}
		seen[x.Key] = true
		if prior.Evidence != x.Evidence || prior.Excerpt != x.Excerpt || prior.WholePreview != x.WholePreview || prior.CollectedAt != x.CollectedAt {
			return out, errors.New("evidence snapshots are immutable")
		}
		if len(x.Evidence.Text) > 256<<10 || len(x.Excerpt) > 256<<10 {
			return out, ErrCollectionLimit
		}
		metadata := x
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
		text += len(x.Evidence.Text) + len(x.Excerpt)
		b, e := json.Marshal(x)
		if e != nil {
			return out, e
		}
		if len(b) > 4<<20 {
			return out, ErrCollectionLimit
		}
		total += len(b)
		raws[i] = string(b)
	}
	if text > maxCollectionText || total > maxCollectionPayload {
		return out, ErrCollectionLimit
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, e = tx.ExecContext(ctx, "UPDATE evidence_collections SET title=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?", title, now, id, rev); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM evidence_collection_entries WHERE collection_id=?", id); e != nil {
		return out, e
	}
	for i, x := range entries {
		if _, e = tx.ExecContext(ctx, "INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json,preview_start_char) VALUES(?,?,?,?,?,?,?)", id, x.Key, i, x.Evidence.SourceID, x.Evidence.ID, raws[i], x.Evidence.TextStartChar); e != nil {
			return out, e
		}
	}
	var aggregate int
	if e = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT sum(length(CAST(snapshot_json AS BLOB))) FROM evidence_collection_entries),0)+COALESCE((SELECT sum(length(CAST(title AS BLOB))) FROM evidence_collections),0)").Scan(&aggregate); e != nil {
		return out, e
	}
	if aggregate > maxCollectionPayload {
		return out, ErrCollectionLimit
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return s.LoadCollection(ctx, id)
}

// SaveCollection atomically applies rename, note edits, reorder/removal and new pins under one revision check.
func (s *Store) SaveCollection(ctx context.Context, id string, rev int64, title string, edits []CollectionEdit) (Collection, error) {
	var out Collection
	if s.db == nil {
		return out, errors.New("index is read-only")
	}
	if rev < 1 || !collectionTitle(title) || len(edits) > maxCollectionEntries {
		return out, ErrCollectionLimit
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var current int64
	if e = tx.QueryRowContext(ctx, "SELECT revision FROM evidence_collections WHERE id=?", id).Scan(&current); errors.Is(e, sql.ErrNoRows) {
		return out, ErrCollectionNotFound
	} else if e != nil {
		return out, e
	}
	if current != rev {
		return out, ErrCollectionConflict
	}
	prior := map[string]CollectionEntry{}
	rows, e := tx.QueryContext(ctx, "SELECT snapshot_json FROM evidence_collection_entries WHERE collection_id=?", id)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var raw string
		var x CollectionEntry
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(raw), &x); e != nil {
			rows.Close()
			return out, e
		}
		prior[x.Key] = x
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return out, e
	}
	rows.Close()
	items := make([]CollectionEntry, 0, len(edits))
	keys := map[string]bool{}
	text, total := 0, 0
	for _, edit := range edits {
		if len(edit.Note) > maxCollectionNote || !utf8.ValidString(edit.Note) {
			return out, ErrCollectionLimit
		}
		var item CollectionEntry
		if edit.Key != "" {
			var ok bool
			item, ok = prior[edit.Key]
			if !ok || keys[edit.Key] {
				return out, errors.New("unknown or repeated entry key")
			}
			keys[edit.Key] = true
			item.Note = edit.Note
		} else {
			if edit.DocumentID == "" || edit.Page < 0 || strings.TrimSpace(edit.Excerpt) == "" || len(edit.Excerpt) > 256<<10 || !utf8.ValidString(edit.Excerpt) {
				return out, errors.New("invalid new evidence passage")
			}
			ev, err := evidenceInWriteTxAt(ctx, tx, edit.DocumentID, edit.Page, edit.TextStartChar)
			if err != nil {
				return out, err
			}
			if edit.OriginalIdentity == "" || edit.OriginalIdentity != ev.OriginalIdentity {
				return out, ErrCollectionSnapshotChanged
			}
			if len(ev.Text) > 256<<10 || !strings.Contains(ev.Text, edit.Excerpt) {
				return out, errors.New("passage is not part of indexed preview")
			}
			if edit.WholePreview && edit.Excerpt != ev.Text {
				return out, errors.New("whole-preview selection must equal the visible preview")
			}
			key, err := newCollectionID()
			if err != nil {
				return out, err
			}
			item = CollectionEntry{Key: key, Evidence: ev, Excerpt: edit.Excerpt, WholePreview: edit.WholePreview, Note: edit.Note, CollectedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			keys[key] = true
		}
		for _, seen := range items {
			if seen.Evidence.ID == item.Evidence.ID && seen.Evidence.SourceID == item.Evidence.SourceID && seen.Evidence.ContentHash == item.Evidence.ContentHash && seen.Evidence.Page == item.Evidence.Page && seen.Excerpt == item.Excerpt {
				return out, errors.New("duplicate evidence passage")
			}
		}
		text += len(item.Evidence.Text) + len(item.Excerpt)
		if text > maxCollectionText {
			return out, ErrCollectionLimit
		}
		metadata := item
		metadata.Evidence.Text = ""
		metadata.Excerpt = ""
		metadata.Note = ""
		mb, err := json.Marshal(metadata)
		if err != nil {
			return out, err
		}
		if len(mb) > 16<<10 {
			return out, ErrCollectionLimit
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return out, err
		}
		if len(raw) > 4<<20 {
			return out, ErrCollectionLimit
		}
		total += len(raw)
		if total > maxCollectionPayload {
			return out, ErrCollectionLimit
		}
		items = append(items, item)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, e = tx.ExecContext(ctx, "UPDATE evidence_collections SET title=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?", title, now, id, rev); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM evidence_collection_entries WHERE collection_id=?", id); e != nil {
		return out, e
	}
	for i, x := range items {
		raw, err := json.Marshal(x)
		if err != nil {
			return out, err
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json,preview_start_char) VALUES(?,?,?,?,?,?,?)", id, x.Key, i, x.Evidence.SourceID, x.Evidence.ID, string(raw), x.Evidence.TextStartChar); e != nil {
			return out, e
		}
	}
	var aggregate int
	if e = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT sum(length(CAST(snapshot_json AS BLOB))) FROM evidence_collection_entries),0)+COALESCE((SELECT sum(length(CAST(title AS BLOB))) FROM evidence_collections),0)").Scan(&aggregate); e != nil {
		return out, e
	}
	if aggregate > maxCollectionPayload {
		return out, ErrCollectionLimit
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return s.LoadCollection(ctx, id)
}

func evidenceInWriteTx(ctx context.Context, tx *sql.Tx, id string, page int) (search.Evidence, error) {
	return evidenceInWriteTxAt(ctx, tx, id, page, 0)
}
func evidenceInWriteTxAt(ctx context.Context, tx *sql.Tx, id string, page int, start int) (search.Evidence, error) {
	var v search.Evidence
	if page < 0 || start < 0 {
		return v, search.ErrPage
	}
	var registration string
	var revision int64
	e := tx.QueryRowContext(ctx, `SELECT d.id,d.title,d.uri,d.path,d.source_id,s.name,s.kind,d.media_type,d.content_hash,d.modified_at,d.page_count,s.registration_token,s.revision FROM documents d JOIN sources s ON s.id=d.source_id WHERE d.id=?`, id).Scan(&v.ID, &v.Title, &v.URI, &v.Path, &v.SourceID, &v.SourceName, &v.SourceKind, &v.MediaType, &v.ContentHash, &v.ModifiedAt, &v.PageCount, &registration, &revision)
	if errors.Is(e, sql.ErrNoRows) {
		return v, search.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	v.OriginalIdentity = originals.Identity(registration, revision, v.ContentHash, v.SourceID, v.ID)
	if v.PageCount > 0 {
		if start != 0 {
			return v, search.ErrPage
		}
		if page == 0 {
			e = tx.QueryRowContext(ctx, "SELECT substr(content,1,65536),length(content)>65536 FROM documents WHERE id=?", id).Scan(&v.Text, &v.Truncated)
		} else if page <= v.PageCount {
			v.Page = page
			e = tx.QueryRowContext(ctx, "SELECT substr(content,1,65536),length(content)>65536 FROM document_pages WHERE document_id=? AND page_number=?", id, page).Scan(&v.Text, &v.Truncated)
			v.URI += fmt.Sprintf("#page=%d", page)
		} else {
			return v, search.ErrPage
		}
	} else {
		if page != 0 {
			return v, search.ErrPage
		}
		var window string
		var total int
		e = tx.QueryRowContext(ctx, "SELECT length(content),substr(content,?,65536) FROM documents WHERE id=?", start+1, id).Scan(&total, &window)
		if e == nil {
			if start > total {
				return v, search.ErrPage
			}
			rs := []rune(window)
			end := len(rs)
			for end > 0 && len(string(rs[:end])) > 256<<10 {
				end--
			}
			v.Text = string(rs[:end])
			v.TextStartChar = start
			v.TextTotalChars = total
			v.Truncated = start > 0 || start+end < total
		}
	}
	return v, e
}
func (s *Store) DeleteCollection(ctx context.Context, id string, rev int64) error {
	if s.db == nil {
		return errors.New("index is read-only")
	}
	r, e := s.db.ExecContext(ctx, "DELETE FROM evidence_collections WHERE id=? AND revision=?", id, rev)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	var x int
	e = s.readers.QueryRowContext(ctx, "SELECT 1 FROM evidence_collections WHERE id=?", id).Scan(&x)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrCollectionNotFound
	}
	if e != nil {
		return e
	}
	return ErrCollectionConflict
}
