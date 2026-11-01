package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
)

const (
	pageTTL       = 5 * time.Minute
	pageDeadline  = 5 * time.Second
	pageJSONLimit = 524288
	pageTokenRaw  = 1 + 16 + 32 + 4 + 8 + 8 + 32
)

type pageRuntime struct {
	key    [32]byte
	epoch  [16]byte
	conn   *sql.Conn
	ctx    context.Context
	cancel context.CancelFunc
	gate   chan struct{} // one executing call plus at most eight queued calls
	serial chan struct{}
	now    func() time.Time
	failed bool // accessed only by the executing page operation
}

func newPageRuntime(parent context.Context) (*pageRuntime, error) {
	p := &pageRuntime{gate: make(chan struct{}, 9), serial: make(chan struct{}, 1)}
	p.now = time.Now
	if _, err := rand.Read(p.key[:]); err != nil {
		return nil, err
	}
	if _, err := rand.Read(p.epoch[:]); err != nil {
		return nil, err
	}
	p.ctx, p.cancel = context.WithCancel(parent)
	return p, nil
}

// Register calls under the same lock that prevents new work during Close.
// Connection acquisition happens later, after bounded and cancellable admission.
func (s *Store) pageState() (*pageRuntime, error) {
	s.pagerMu.Lock()
	defer s.pagerMu.Unlock()
	if s.pagerClosed {
		return nil, search.ErrPageClosed
	}
	if s.pager != nil {
		s.pagerWG.Add(1)
		return s.pager, nil
	}
	p, err := newPageRuntime(context.Background())
	if err != nil {
		return nil, errors.Join(search.ErrPageUnavailable, err)
	}
	s.pager = p
	if s.pageNow != nil {
		p.now = s.pageNow
	}
	s.pagerWG.Add(1)
	return p, nil
}

func (s *Store) SearchPage(ctx context.Context, input search.PageRequest) (search.PageResponse, error) {
	response := search.PageResponse{Results: []search.PageResult{}, Limit: input.Request.Limit, WindowLimit: search.PageWindowLimit}
	if ctx == nil {
		ctx = context.Background()
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, pageDeadline)
	defer cancel()
	if err := deadlineCtx.Err(); err != nil {
		return response, err
	}
	req := input.Request
	if req.Limit == 0 {
		req.Limit = 20
	}
	if req.Mode == "" {
		req.Mode = "literal"
	}
	if req.Format == "" {
		req.Format = "all"
	}
	response.Limit = req.Limit
	if err := search.ValidateRequest(req); err != nil {
		return response, err
	}
	if req.Limit < 1 || req.Limit > 100 {
		return response, search.ErrLimit
	}
	// Validate the original path first: '/' and 'docs//' must not become valid
	// (or lose their filter) merely by removing a trailing slash.
	req.PathPrefix = trimPathSlash(req.PathPrefix)
	p, err := s.pageState()
	if err != nil {
		return response, err
	}
	defer s.pagerWG.Done()
	select {
	case p.gate <- struct{}{}:
	case <-deadlineCtx.Done():
		return response, deadlineCtx.Err()
	case <-p.ctx.Done():
		return response, search.ErrPageClosed
	default:
		return response, search.ErrPageBusy
	}
	defer func() { <-p.gate }()
	select {
	case p.serial <- struct{}{}:
	case <-deadlineCtx.Done():
		return response, deadlineCtx.Err()
	case <-p.ctx.Done():
		return response, search.ErrPageClosed
	}
	defer func() { <-p.serial }()
	ctx, opCancel := context.WithCancel(deadlineCtx)
	stop := context.AfterFunc(p.ctx, opCancel)
	defer func() { stop(); opCancel() }()
	if err := pageOperationErr(p, ctx); err != nil {
		return response, err
	}
	if p.failed {
		return response, search.ErrPageUnavailable
	}
	digest := pageDigest(req)
	offset := 0
	fence := int64(-1)
	expires := p.now().Add(pageTTL).UnixNano()
	if input.Cursor != "" {
		tok, err := p.decodeCursor(input.Cursor)
		if err != nil {
			return response, err
		}
		if tok.epoch != p.epoch {
			return response, search.ErrCursorSessionChanged
		}
		expectedMAC := p.mac(tok.body)
		if !hmac.Equal(tok.mac[:], expectedMAC[:]) {
			return response, search.ErrCursorInvalid
		}
		if tok.digest != digest {
			return response, search.ErrCursorMismatch
		}
		if p.now().UnixNano() >= tok.expiry {
			return response, search.ErrCursorExpired
		}
		offset, fence, expires = int(tok.offset), tok.fence, tok.expiry
	}
	if p.conn == nil {
		conn, err := s.readers.Conn(ctx)
		if err != nil {
			return response, s.pageDBErr(p, ctx, err)
		}
		p.conn = conn
	}
	var before int64
	if err := p.conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&before); err != nil {
		return response, s.pageDBErr(p, ctx, err)
	}
	if fence >= 0 && before != fence {
		return response, search.ErrCursorStale
	}
	tx, err := p.conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return response, s.pageDBErr(p, ctx, err)
	}
	page, queryErr := searchPageTx(ctx, tx, req, offset)
	rollbackErr := tx.Rollback()
	if queryErr != nil {
		return response, s.pageDBErr(p, ctx, queryErr)
	}
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return response, s.pageDBErr(p, ctx, rollbackErr)
	}
	var after int64
	if err := p.conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&after); err != nil {
		return response, s.pageDBErr(p, ctx, err)
	}
	if before != after {
		return response, search.ErrCursorStale
	}
	if fence >= 0 && after != fence {
		return response, search.ErrCursorStale
	}
	page.Offset, page.Limit, page.WindowLimit = offset, req.Limit, search.PageWindowLimit
	return fitSearchPage(ctx, p, page, digest, after, expires)
}

// Encode rows once, then size candidate envelopes with an empty results array.
// This avoids repeatedly serializing large prefixes while enforcing the budget
// against the final JSON (including the transport's newline).
func fitSearchPage(ctx context.Context, p *pageRuntime, page search.PageResponse, digest [32]byte, fence, expiry int64) (search.PageResponse, error) {
	rows := page.Results
	page.Results = []search.PageResult{}
	var selected search.PageResponse
	prefixBytes, best := 0, -1
	for n := 0; n <= len(rows); n++ {
		if err := pageOperationErr(p, ctx); err != nil {
			return search.PageResponse{}, err
		}
		if n > 0 {
			b, err := json.Marshal(rows[n-1])
			if err != nil {
				return search.PageResponse{}, err
			}
			prefixBytes += len(b)
		}
		page.NextCursor, page.EndReason = nil, nil
		page.Returned = n
		end := page.Offset + n
		if end >= page.Total {
			reason := "complete"
			page.EndReason = &reason
		}
		if end >= search.PageWindowLimit && end < page.Total {
			reason := "window_limit"
			page.EndReason = &reason
		}
		if page.EndReason == nil {
			token := p.encodeCursor(digest, end, fence, expiry)
			page.NextCursor = &token
		}
		b, e := json.Marshal(page)
		if e != nil {
			return search.PageResponse{}, e
		}
		commas := max(0, n-1)
		if len(b)+prefixBytes+commas+1 <= pageJSONLimit && (n > 0 || page.Total <= page.Offset) {
			selected, best = page, n
		}
	}
	if err := pageOperationErr(p, ctx); err != nil {
		return search.PageResponse{}, err
	}
	if best < 0 {
		return search.PageResponse{}, search.ErrPageResultTooLarge
	}
	selected.Results = rows[:best]
	if selected.Results == nil {
		selected.Results = []search.PageResult{}
	}
	b, err := json.Marshal(selected)
	if err != nil {
		return search.PageResponse{}, err
	}
	if err := pageOperationErr(p, ctx); err != nil {
		return search.PageResponse{}, err
	}
	if len(b)+1 > pageJSONLimit {
		return search.PageResponse{}, search.ErrPageResultTooLarge
	}
	return selected, nil
}

type pageToken struct {
	epoch  [16]byte
	digest [32]byte
	offset uint32
	fence  int64
	expiry int64
	mac    [32]byte
	body   []byte
}

func (p *pageRuntime) decodeCursor(encoded string) (pageToken, error) {
	var token pageToken
	if len(encoded) == 0 || len(encoded) > 4096 || len(encoded) != base64.RawURLEncoding.EncodedLen(pageTokenRaw) {
		return token, search.ErrCursorInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(b) != pageTokenRaw || base64.RawURLEncoding.EncodeToString(b) != encoded {
		return token, search.ErrCursorInvalid
	}
	if b[0] != 1 {
		return token, search.ErrCursorInvalid
	}
	copy(token.epoch[:], b[1:17])
	copy(token.digest[:], b[17:49])
	token.offset = binary.BigEndian.Uint32(b[49:53])
	token.fence = int64(binary.BigEndian.Uint64(b[53:61]))
	token.expiry = int64(binary.BigEndian.Uint64(b[61:69]))
	if token.offset >= search.PageWindowLimit || token.fence < 0 {
		return token, search.ErrCursorInvalid
	}
	copy(token.mac[:], b[69:])
	token.body = b[:69]
	return token, nil
}
func (p *pageRuntime) mac(body []byte) [32]byte {
	h := hmac.New(sha256.New, p.key[:])
	_, _ = h.Write(body)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func (p *pageRuntime) encodeCursor(digest [32]byte, offset int, fence, expiry int64) string {
	b := make([]byte, 69)
	b[0] = 1
	copy(b[1:17], p.epoch[:])
	copy(b[17:49], digest[:])
	binary.BigEndian.PutUint32(b[49:53], uint32(offset))
	binary.BigEndian.PutUint64(b[53:61], uint64(fence))
	binary.BigEndian.PutUint64(b[61:69], uint64(expiry))
	m := p.mac(b)
	b = append(b, m[:]...)
	return base64.RawURLEncoding.EncodeToString(b)
}
func pageDigest(r search.Request) [32]byte {
	h := sha256.New()
	fields := []string{r.Query, r.Mode, r.SourceID, r.Format, r.PathPrefix, r.TitleContains}
	for _, v := range fields {
		writeField(h, v)
	}
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(r.Limit))
	_, _ = h.Write(n[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func writeField(h interface{ Write([]byte) (int, error) }, value string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(value)))
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(value))
}
func trimPathSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] == '/' {
		return s[:len(s)-1]
	}
	return s
}
func pageOperationErr(p *pageRuntime, ctx context.Context) error {
	if p.ctx.Err() != nil {
		return search.ErrPageClosed
	}
	return ctx.Err()
}
func (s *Store) pageDBErr(p *pageRuntime, ctx context.Context, err error) error {
	if interrupted := pageOperationErr(p, ctx); interrupted != nil {
		return interrupted
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) {
		// A new connection has an unrelated data_version counter. Fail this
		// runtime until Store reopen rather than accepting its old cursors.
		p.failed = true
	}
	var validation *search.ValidationError
	if errors.As(err, &validation) || errors.Is(err, search.ErrLimit) || errors.Is(err, search.ErrQuery) {
		return err
	}
	return errors.Join(search.ErrPageUnavailable, err)
}

var _ search.PageEngine = (*Store)(nil)
