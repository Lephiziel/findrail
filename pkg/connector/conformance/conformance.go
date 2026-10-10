// Package conformance provides bounded, test-local checks for experimental
// full-inventory connectors. ProfileID identifies this test profile only; it
// is not an API compatibility promise.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/pkg/connector"
)

// ProfileID is the identifier for the observable full-inventory test profile.
const ProfileID = "full-inventory-v1"

// Limits bound all retained callback data. Pages are included in content and
// aggregate accounting; metadata includes source fields and page-count overhead.
type Limits struct{ Documents, ContentPerDocument, ContentTotal, MetadataPerDocument, MetadataTotal, Pages int }

var DefaultLimits = Limits{1000, 256 << 10, 4 << 20, 16 << 10, 4 << 20, 1000}

// Failure is safe for routine diagnostics: it never contains adapter strings or document data.
type Failure struct {
	Code, Check    string
	Ordinal, Count int
}

// ScanError wraps an adapter error without including its potentially sensitive
// message in Error. Unwrap preserves errors.Is/errors.As inspection.
type ScanError struct{ Cause error }

func (e *ScanError) Error() string { return "connector conformance scan failed" }
func (e *ScanError) Unwrap() error { return e.Cause }

func (f *Failure) Error() string {
	return fmt.Sprintf("connector conformance %s (%s), ordinal=%d count=%d", f.Check, f.Code, f.Ordinal, f.Count)
}

// Options configure finite observations. Zero fields are invalid; use DefaultLimits explicitly.
type Options struct {
	Limits  Limits
	Timeout time.Duration
}

func DefaultOptions() Options { return Options{Limits: DefaultLimits, Timeout: 5 * time.Second} }
func (o Options) validate() error {
	l := o.Limits
	if o.Timeout <= 0 || l.Documents <= 0 || l.ContentPerDocument <= 0 || l.ContentTotal <= 0 || l.MetadataPerDocument <= 0 || l.MetadataTotal <= 0 || l.Pages <= 0 {
		return errors.New("invalid conformance limits")
	}
	return nil
}

// Observe runs one bounded scan and checks its emitted inventory. It retains no
// more than the configured budgets and preserves scan errors for errors.Is/As.
func Observe(ctx context.Context, c connector.Connector, o Options) ([]connector.Document, connector.Report, error) {
	if err := o.validate(); err != nil {
		return nil, connector.Report{}, err
	}
	if c == nil {
		return nil, connector.Report{}, errors.New("nil connector")
	}
	if err := ctx.Err(); err != nil {
		return nil, connector.Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	src := c.Source()
	if src.ID == "" || src.Kind == "" || src.Name == "" {
		return nil, connector.Report{}, fail("source_identity", "source", 0, 0)
	}
	if c.Source() != src {
		return nil, connector.Report{}, fail("source_unstable", "source", 0, 0)
	}
	seen := map[string]bool{}
	docs := make([]connector.Document, 0)
	contentTotal, metaTotal := 0, 0
	var active atomic.Bool
	var mu sync.Mutex
	report, err := c.Scan(ctx, func(d connector.Document) error {
		if !active.CompareAndSwap(false, true) {
			return fail("concurrent_emit", "callbacks", 0, len(docs))
		}
		defer active.Store(false)
		mu.Lock()
		defer mu.Unlock()
		if e := ctx.Err(); e != nil {
			return e
		}
		n := len(docs) + 1
		l := o.Limits
		if n > l.Documents {
			return fail("limit_documents", "capture", n, n)
		}
		if d.ID == "" || d.SourceID != src.ID || seen[d.ID] {
			return fail("document_identity", "identity", n, n)
		}
		seen[d.ID] = true
		meta := len(d.ID) + len(d.SourceID) + len(d.Title) + len(d.URI) + len(d.Path) + len(d.Hash) + len(d.MediaType)
		body := len(d.Content)
		pages := 0
		if !utf8.ValidString(d.ID + d.SourceID + d.Title + d.URI + d.Path + d.Hash + d.MediaType + d.Content) {
			return fail("invalid_utf8", "utf8", n, n)
		}
		for _, p := range d.Pages {
			pages++
			body += len(p.Text)
			meta += 16
			if p.Number != pages {
				return fail("page_sequence", "pages", n, pages)
			}
			if !utf8.ValidString(p.Text) {
				return fail("invalid_utf8", "pages", n, pages)
			}
		}
		if body > l.ContentPerDocument || contentTotal+body > l.ContentTotal || meta > l.MetadataPerDocument || metaTotal+meta > l.MetadataTotal || pages > l.Pages {
			return fail("limit_capture", "capture", n, n)
		}
		if d.Hash == "" {
			return fail("missing_hash", "provenance", n, n)
		}
		if d.URI == "" || d.Path == "" || d.Title == "" {
			return fail("missing_provenance", "provenance", n, n)
		}
		if d.SizeBytes < 0 {
			return fail("negative_size", "sizes", n, n)
		}
		contentTotal += body
		metaTotal += meta
		d.Pages = append([]connector.Page(nil), d.Pages...)
		docs = append(docs, d)
		return nil
	})
	if err != nil {
		return docs, report, &ScanError{Cause: err}
	}
	if e := ctx.Err(); e != nil {
		return docs, report, e
	}
	if report.Seen != len(docs) {
		return docs, report, fail("seen_mismatch", "report", 0, report.Seen)
	}
	if report.Seen < 0 || report.Skipped < 0 || report.SkippedPDF < 0 || report.SkippedDOCX < 0 || report.SkippedPDF > report.Skipped || report.SkippedDOCX > report.Skipped {
		return docs, report, fail("invalid_report", "report", 0, report.Seen)
	}
	return docs, report, nil
}
func fail(code, check string, ordinal, count int) error { return &Failure{code, check, ordinal, count} }

// Hooks are optional fixture mutations; absent hooks are visibly skipped by Run.
type Hooks struct{ UpdateContent, UpdateMetadata, DeleteOne, Empty, FailLate, Recover, SecondSource func() connector.Connector }

// Run executes bounded mandatory observations and names unconfigured fixture cases as skipped.
func Run(t *testing.T, factory func() connector.Connector, opts Options, hooks *Hooks) {
	t.Helper()
	if err := opts.validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		hook func() connector.Connector
	}{{"inventory", factory}, {"content_update", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.UpdateContent })}, {"metadata_update", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.UpdateMetadata })}, {"single_deletion", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.DeleteOne })}, {"remove_all", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.Empty })}, {"late_failure", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.FailLate })}, {"recovery", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.Recover })}, {"second_source", hookValue(hooks, func(h *Hooks) func() connector.Connector { return h.SecondSource })}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.hook == nil {
				t.Skip("optional fixture hook not configured")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
			defer cancel()
			_, _, err := Observe(ctx, tc.hook(), opts)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func hookValue(h *Hooks, f func(*Hooks) func() connector.Connector) func() connector.Connector {
	if h == nil {
		return nil
	}
	return f(h)
}

// ExampleObserve illustrates direct checks without exposing content in errors.
func ExampleObserve() { /* See the separate-module example for an executable adapter. */ }
