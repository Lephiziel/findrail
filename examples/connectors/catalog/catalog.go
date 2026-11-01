// Package catalog is a bounded educational read-only paginated catalogue adapter.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/pkg/connector"
)

const pageLimit = 1 << 20

// Config requires an explicit HTTPS origin, collection and finite request
// timeout. Cleartext is limited to opted-in numeric loopback fixtures.
type Config struct {
	Origin, Collection string
	AllowLoopbackHTTP  bool
	RequestTimeout     time.Duration
}
type Connector struct {
	source     connector.Source
	origin     string
	collection string
	client     *http.Client
}

// UnavailableError is a safe example failure for access-denied responses.
// It intentionally excludes remote response bodies and configured URLs.
type UnavailableError struct{ Status int }

func (e *UnavailableError) Error() string { return "catalogue is unavailable or access was denied" }

type wirePage struct {
	Collection string     `json:"collection"`
	Snapshot   string     `json:"snapshot"`
	Next       string     `json:"next"`
	Complete   bool       `json:"complete"`
	Items      []wireItem `json:"items"`
}

func (p *wirePage) UnmarshalJSON(data []byte) error {
	type alias wirePage
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, required := range []string{"collection", "snapshot", "complete", "items"} {
		if !hasField(fields, required) {
			return errors.New("missing required page field")
		}
	}
	*p = wirePage(v)
	if p.Items == nil {
		return errors.New("items must be an array")
	}
	return nil
}

type wireItem struct{ ID, Title, Path, URI, Body, Modified string }

func (i *wireItem) UnmarshalJSON(data []byte) error {
	type alias wireItem
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, required := range []string{"id", "title", "path", "uri", "body", "modified"} {
		if !hasField(fields, required) {
			return errors.New("missing required resource field")
		}
	}
	*i = wireItem(v)
	return nil
}
func hasField(fields map[string]json.RawMessage, want string) bool {
	for key := range fields {
		if strings.EqualFold(key, want) {
			return true
		}
	}
	return false
}

func New(cfg Config) (*Connector, error) {
	u, e := url.Parse(cfg.Origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(cfg.Origin, "#") || strings.Contains(cfg.Origin, "?") || u.Path != "" {
		return nil, errors.New("invalid catalogue origin")
	}
	if u.Scheme != "https" && !(cfg.AllowLoopbackHTTP && u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil, errors.New("catalogue origin must use HTTPS (loopback HTTP requires explicit opt-in)")
	}
	if cfg.Collection == "" || len(cfg.Collection) > 128 || !utf8.ValidString(cfg.Collection) || cfg.Collection == "." || cfg.Collection == ".." || strings.ContainsAny(cfg.Collection, "/\\?#") {
		return nil, errors.New("invalid collection")
	}
	for _, r := range cfg.Collection {
		if r == 0 || r < 0x20 || r == 0x7f {
			return nil, errors.New("invalid collection")
		}
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 || timeout > 5*time.Second {
		return nil, errors.New("explicit request timeout must be at most five seconds")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, IdleConnTimeout: 30 * time.Second, DisableKeepAlives: true, MaxConnsPerHost: 1}
	cl := &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sum := sha256.Sum256([]byte("catalog-source-v1\x00" + cfg.Origin + "\x00" + cfg.Collection))
	id := hex.EncodeToString(sum[:])
	return &Connector{connector.Source{ID: id, Kind: "educational-catalog", Name: cfg.Collection, Root: cfg.Origin + "/collections/" + url.PathEscape(cfg.Collection), MaxTextBytes: 1 << 20}, strings.TrimSuffix(cfg.Origin, "/"), cfg.Collection, cl}, nil
}
func isLoopback(host string) bool             { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
func (c *Connector) Source() connector.Source { return c.source }
func (c *Connector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	var report connector.Report
	if emit == nil {
		return report, errors.New("catalogue consumer callback is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cursor, snapshot := "", ""
	cursors := map[string]bool{}
	ids := map[string]bool{}
	total, responseTotal := 0, 0
	for page := 0; page < 100; page++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		endpoint := c.origin + "/v1/collections/" + url.PathEscape(c.collection) + "/resources"
		if cursor != "" {
			endpoint += "?cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return report, err
		}
		res, err := c.client.Do(req)
		if err != nil {
			return report, &requestFailure{cause: err}
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, pageLimit+1))
		closeErr := res.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			return report, errors.New("catalogue response read failed")
		}
		if closeErr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			return report, errors.New("catalogue response close failed")
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if len(body) > pageLimit {
			return report, errors.New("catalogue page exceeds response limit")
		}
		responseTotal += len(body)
		if responseTotal > 4<<20 {
			return report, errors.New("catalogue scan response budget exceeded")
		}
		if !utf8.Valid(body) {
			return report, errors.New("catalogue response is not valid UTF-8")
		}
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return report, &UnavailableError{Status: res.StatusCode}
		}
		if res.StatusCode != 200 {
			return report, fmt.Errorf("catalogue request failed with status %d", res.StatusCode)
		}
		var p wirePage
		dec := json.NewDecoder(strings.NewReader(string(body)))
		if err = dec.Decode(&p); err != nil {
			return report, errors.New("malformed catalogue page")
		}
		var extra any
		if err = dec.Decode(&extra); err != io.EOF {
			return report, errors.New("trailing catalogue data")
		}
		if p.Collection != c.collection || p.Snapshot == "" {
			return report, errors.New("catalogue page identity mismatch")
		}
		if snapshot != "" && snapshot != p.Snapshot {
			return report, errors.New("catalogue snapshot changed")
		}
		snapshot = p.Snapshot
		if err := ctx.Err(); err != nil {
			return report, err
		}
		for _, item := range p.Items {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			metadataBytes := len(item.ID) + len(item.Title) + len(item.Path) + len(item.URI) + len(item.Modified)
			if item.ID == "" || ids[item.ID] || item.Title == "" || item.Path == "" || strings.HasPrefix(item.Path, "/") || strings.Contains(item.Path, "\\") || !utf8.ValidString(item.ID+item.Title+item.Path+item.URI+item.Body+item.Modified) || len(item.Body) > 256<<10 || metadataBytes > 16<<10 {
				return report, errors.New("invalid catalogue resource")
			}
			for _, r := range item.ID + item.Title + item.Path + item.Modified {
				if r == 0 || r < 0x20 || r == 0x7f {
					return report, errors.New("invalid catalogue metadata")
				}
			}
			if len(item.Path) >= 2 && ((item.Path[0] >= 'a' && item.Path[0] <= 'z') || (item.Path[0] >= 'A' && item.Path[0] <= 'Z')) && item.Path[1] == ':' {
				return report, errors.New("catalogue path must be relative")
			}
			for _, part := range strings.Split(item.Path, "/") {
				if part == "." || part == ".." || part == "" {
					return report, errors.New("invalid catalogue path")
				}
			}
			u, e := url.Parse(item.URI)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return report, errors.New("invalid catalogue provenance")
			}
			total += len(item.Body)
			if total > 4<<20 || len(ids) >= 1000 {
				return report, errors.New("catalogue scan limit exceeded")
			}
			ids[item.ID] = true
			h := sha256.Sum256([]byte("catalog-content-v1\x00" + item.Body))
			idHash := sha256.Sum256([]byte(c.source.ID + "\x00" + item.ID))
			modified, e := time.Parse(time.RFC3339, item.Modified)
			if e != nil {
				return report, errors.New("invalid catalogue timestamp")
			}
			d := connector.Document{ID: hex.EncodeToString(idHash[:]), SourceID: c.source.ID, Title: item.Title, URI: item.URI, Path: item.Path, Content: item.Body, Hash: "catalog-content-v1:" + hex.EncodeToString(h[:]), SizeBytes: int64(len(item.Body)), ModifiedAt: modified, MediaType: "text/plain"}
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if err := emit(d); err != nil {
				return report, err
			}
			report.Seen++
		}
		if p.Complete {
			if p.Next != "" {
				return report, errors.New("complete page has next cursor")
			}
			if err := ctx.Err(); err != nil {
				return report, err
			}
			return report, nil
		}
		if p.Next == "" || len(p.Next) > 256 || strings.ContainsAny(p.Next, "/?#\\") || cursors[p.Next] {
			return report, errors.New("invalid or looping catalogue cursor")
		}
		cursors[p.Next] = true
		cursor = p.Next
	}
	return report, errors.New("catalogue page limit exceeded")
}

type requestFailure struct{ cause error }

func (e *requestFailure) Error() string { return "catalogue request failed" }
func (e *requestFailure) Unwrap() error { return e.cause }
