// Package conformance provides bounded, test-local checks for experimental
// full-inventory connectors. ProfileID identifies this test profile only; it
// is not an API compatibility promise.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
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

const (
	maxDocuments        = 1000
	maxContentDocument  = 256 << 10
	maxContentTotal     = 4 << 20
	maxMetadataDocument = 16 << 10
	maxMetadataTotal    = 4 << 20
	maxPages            = 1000
)

// Failure is safe for routine diagnostics: it never contains adapter strings or document data.
type Failure struct {
	Code, Check    string
	Ordinal, Count int
}

// ScanError wraps an adapter error without including its potentially sensitive
// message in Error. Unwrap preserves errors.Is/errors.As inspection.
type ScanError struct {
	Cause error
	Check *Failure
}

func (e *ScanError) Error() string {
	if e.Check != nil {
		return e.Check.Error()
	}
	return "connector conformance scan failed"
}
func (e *ScanError) Unwrap() []error {
	if e.Check != nil {
		return []error{e.Cause, e.Check}
	}
	return []error{e.Cause}
}

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
	if o.Timeout <= 0 || o.Timeout > 30*time.Second || l.Documents <= 0 || l.Documents > maxDocuments || l.ContentPerDocument <= 0 || l.ContentPerDocument > maxContentDocument || l.ContentTotal <= 0 || l.ContentTotal > maxContentTotal || l.MetadataPerDocument <= 0 || l.MetadataPerDocument > maxMetadataDocument || l.MetadataTotal <= 0 || l.MetadataTotal > maxMetadataTotal || l.Pages <= 0 || l.Pages > maxPages {
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
	if src.ID == "" || src.Kind == "" || src.Name == "" || !allValidStrings(src.ID, src.Kind, src.Name, src.Root) {
		return nil, connector.Report{}, fail("source_identity", "source", 0, 0)
	}
	if c.Source() != src {
		return nil, connector.Report{}, fail("source_unstable", "source", 0, 0)
	}
	seen := map[string]bool{}
	docs := make([]connector.Document, 0)
	contentTotal, metaTotal, pageTotal := 0, 0, 0
	var captureErr error
	sourceMeta, ok := boundedStringBytes(o.Limits.MetadataTotal, src.ID, src.Kind, src.Name, src.Root)
	if !ok {
		return nil, connector.Report{}, fail("limit_capture", "source_metadata", 0, sourceMeta)
	}
	metaTotal = sourceMeta
	var active atomic.Bool
	var mu sync.Mutex
	reject := func(err error) error { captureErr = err; return err }
	report, err := c.Scan(ctx, func(d connector.Document) error {
		if !active.CompareAndSwap(false, true) {
			mu.Lock()
			defer mu.Unlock()
			if captureErr == nil {
				captureErr = fail("concurrent_emit", "callbacks", 0, 0)
			}
			return captureErr
		}
		defer active.Store(false)
		mu.Lock()
		defer mu.Unlock()
		if captureErr != nil {
			return captureErr
		}
		if e := ctx.Err(); e != nil {
			return reject(e)
		}
		n := len(docs) + 1
		l := o.Limits
		if n > l.Documents {
			return reject(fail("limit_documents", "capture", n, n))
		}
		if d.ID == "" || d.SourceID != src.ID || seen[d.ID] {
			return reject(fail("document_identity", "identity", n, n))
		}
		seen[d.ID] = true
		stringFields := []string{d.ID, d.SourceID, d.Title, d.URI, d.Path, d.Hash, d.MediaType, d.Content}
		if !allValidStrings(stringFields...) {
			return reject(fail("invalid_utf8", "utf8", n, n))
		}
		meta, ok := boundedStringBytes(l.MetadataPerDocument, d.ID, d.SourceID, d.Title, d.URI, d.Path, d.Hash, d.MediaType)
		if !ok {
			return reject(fail("limit_capture", "metadata", n, n))
		}
		body := len(d.Content)
		if body > l.ContentPerDocument {
			return reject(fail("limit_capture", "content", n, n))
		}
		pages := 0
		for _, p := range d.Pages {
			pages++
			if len(p.Text) > l.ContentPerDocument-body {
				return reject(fail("limit_capture", "content", n, n))
			}
			body += len(p.Text)
			if meta > l.MetadataPerDocument-16 {
				return reject(fail("limit_capture", "metadata", n, n))
			}
			meta += 16
			if p.Number != pages {
				return reject(fail("page_sequence", "pages", n, pages))
			}
			if !utf8.ValidString(p.Text) {
				return reject(fail("invalid_utf8", "pages", n, pages))
			}
		}
		if len(d.Pages) > 0 && d.MediaType != "application/pdf" {
			return reject(fail("pages_on_non_pdf", "pages", n, len(d.Pages)))
		}
		if body > l.ContentPerDocument || body > l.ContentTotal-contentTotal || meta > l.MetadataPerDocument || meta > l.MetadataTotal-metaTotal || pages > l.Pages-pageTotal {
			return reject(fail("limit_capture", "capture", n, n))
		}
		if d.Hash == "" {
			return reject(fail("missing_hash", "provenance", n, n))
		}
		if d.URI == "" || d.Path == "" || d.Title == "" {
			return reject(fail("missing_provenance", "provenance", n, n))
		}
		if d.SizeBytes < 0 {
			return reject(fail("negative_size", "sizes", n, n))
		}
		contentTotal += body
		metaTotal += meta
		pageTotal += pages
		d.Pages = append([]connector.Page(nil), d.Pages...)
		docs = append(docs, d)
		return nil
	})
	mu.Lock()
	observedCaptureErr := captureErr
	mu.Unlock()
	if err != nil {
		if invalidReport(report) {
			return docs, report, &ScanError{Cause: err, Check: fail("invalid_report", "report", 0, report.Seen).(*Failure)}
		}
		return docs, report, &ScanError{Cause: err}
	}
	if observedCaptureErr != nil {
		return docs, report, observedCaptureErr
	}
	if e := ctx.Err(); e != nil {
		return docs, report, e
	}
	if invalidReport(report) {
		return docs, report, fail("invalid_report", "report", 0, report.Seen)
	}
	if report.Seen != len(docs) {
		return docs, report, fail("seen_mismatch", "report", 0, report.Seen)
	}
	return docs, report, nil
}

func invalidReport(report connector.Report) bool {
	return report.Seen < 0 || report.Skipped < 0 || report.SkippedPDF < 0 || report.SkippedDOCX < 0 || report.SkippedPDF > report.Skipped || report.SkippedDOCX > report.Skipped
}
func fail(code, check string, ordinal, count int) error { return &Failure{code, check, ordinal, count} }

func boundedStringBytes(limit int, values ...string) (int, bool) {
	total := 0
	for _, value := range values {
		if len(value) > limit-total {
			return 0, false
		}
		total += len(value)
	}
	return total, true
}
func allValidStrings(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

// CheckStableInventory observes two fresh fixtures and compares normalized
// document inventories by ID, not callback order.
func CheckStableInventory(ctx context.Context, factory func() connector.Connector, opts Options) error {
	if factory == nil {
		return errors.New("nil connector factory")
	}
	a, _, err := Observe(ctx, factory(), opts)
	if err != nil {
		return err
	}
	b, _, err := Observe(ctx, factory(), opts)
	if err != nil {
		return err
	}
	sort.Slice(a, func(i, j int) bool { return a[i].ID < a[j].ID })
	sort.Slice(b, func(i, j int) bool { return b[i].ID < b[j].ID })
	if !reflect.DeepEqual(a, b) {
		return fail("inventory_unstable", "inventory", 0, len(b))
	}
	return nil
}

// CheckExpectedInventory rejects a successful but fixture-incomplete inventory.
// Expected values must be synthetic and are compared after sorting by stable ID.
func CheckExpectedInventory(ctx context.Context, c connector.Connector, expected []connector.Document, opts Options) error {
	actual, _, err := Observe(ctx, c, opts)
	if err != nil {
		return err
	}
	want := append([]connector.Document(nil), expected...)
	sort.Slice(actual, func(i, j int) bool { return actual[i].ID < actual[j].ID })
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
	if !reflect.DeepEqual(actual, want) {
		return fail("incomplete_inventory", "expected_inventory", 0, len(actual))
	}
	return nil
}

// CheckSourceIsolation observes two source fixtures and rejects equal source or
// document IDs across them.
func CheckSourceIsolation(ctx context.Context, first, second connector.Connector, opts Options) error {
	a, _, err := Observe(ctx, first, opts)
	if err != nil {
		return err
	}
	b, _, err := Observe(ctx, second, opts)
	if err != nil {
		return err
	}
	if first.Source().ID == second.Source().ID {
		return fail("source_collision", "source_isolation", 0, 0)
	}
	ids := make(map[string]bool, len(a))
	for _, d := range a {
		ids[d.ID] = true
	}
	for i, d := range b {
		if ids[d.ID] {
			return fail("document_collision", "source_isolation", i+1, len(b))
		}
	}
	return nil
}

// CheckContentHashChange verifies that changed indexed body or page content for
// an unchanged document identity changes its snapshot hash.
func CheckContentHashChange(before, after connector.Document) error {
	if before.ID != after.ID || before.SourceID != after.SourceID {
		return fail("identity_changed", "hash_change", 0, 0)
	}
	changed := before.Content != after.Content || !reflect.DeepEqual(before.Pages, after.Pages)
	if !changed {
		return fail("content_not_changed", "hash_change", 0, 0)
	}
	if before.Hash == after.Hash {
		return fail("stale_hash", "hash_change", 0, 0)
	}
	return nil
}

// CheckMetadataObservable verifies a metadata-only fixture change remains
// visible in the emitted document; it intentionally does not require a hash change.
func CheckMetadataObservable(before, after connector.Document) error {
	if before.ID != after.ID || before.SourceID != after.SourceID {
		return fail("identity_changed", "metadata_change", 0, 0)
	}
	if before.Content != after.Content || !reflect.DeepEqual(before.Pages, after.Pages) {
		return fail("not_metadata_only", "metadata_change", 0, 0)
	}
	if before.Title == after.Title && before.Path == after.Path && before.URI == after.URI && before.MediaType == after.MediaType && before.ModifiedAt.Equal(after.ModifiedAt) {
		return fail("metadata_not_observable", "metadata_change", 0, 0)
	}
	return nil
}

// CheckPreCancellation requires a canceled scan to return a recognizable
// cancellation error before emitting any document.
func CheckPreCancellation(c connector.Connector) error {
	if c == nil {
		return errors.New("nil connector")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var count atomic.Int32
	_, err := c.Scan(ctx, func(connector.Document) error { count.Add(1); return nil })
	if count.Load() != 0 {
		return fail("cancel_emitted", "cancellation", 0, int(count.Load()))
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return fail("cancel_ignored", "cancellation", 0, 0)
	}
	return nil
}

// CheckCancellationAfterFirst cancels at the first accepted callback and
// requires the connector to stop before any future emission. The adapter must
// cooperate with context; an uncooperative call cannot safely be terminated.
func CheckCancellationAfterFirst(parent context.Context, c connector.Connector, opts Options) error {
	if err := opts.validate(); err != nil {
		return err
	}
	if c == nil {
		return errors.New("nil connector")
	}
	ctx, cancel := context.WithTimeout(parent, opts.Timeout)
	defer cancel()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var calls atomic.Int32
	_, err := c.Scan(ctx, func(connector.Document) error {
		n := calls.Add(1)
		if n == 1 {
			stop()
			return nil
		}
		return ctx.Err()
	})
	if calls.Load() == 0 {
		return fail("cancel_no_document", "cancellation_after_emit", 0, 0)
	}
	if calls.Load() > 1 {
		return fail("cancel_continued", "cancellation_after_emit", 0, int(calls.Load()))
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return fail("cancel_ignored", "cancellation_after_emit", 0, int(calls.Load()))
	}
	return nil
}

// CheckCancellationRecovery cancels after an accepted document, then requires
// a subsequent fresh scan on the same connector to succeed.
func CheckCancellationRecovery(ctx context.Context, c connector.Connector, opts Options) error {
	if err := CheckCancellationAfterFirst(ctx, c, opts); err != nil {
		return err
	}
	if _, _, err := Observe(ctx, c, opts); err != nil {
		return fail("recovery_failed", "cancellation_recovery", 0, 0)
	}
	return nil
}

// CheckConsumerIsolation injects a consumer sentinel and verifies that a later
// scan on the same connector succeeds without retaining prior callback state.
func CheckConsumerIsolation(ctx context.Context, c connector.Connector, opts Options) error {
	if err := CheckCallbackError(ctx, c, opts); err != nil {
		return err
	}
	if _, _, err := Observe(ctx, c, opts); err != nil {
		return fail("consumer_state_leaked", "consumer_isolation", 0, 0)
	}
	return nil
}

// CheckCallbackError verifies a consumer sentinel remains recognizable and
// stops callback enumeration at its first rejection.
func CheckCallbackError(ctx context.Context, c connector.Connector, opts Options) error {
	if err := opts.validate(); err != nil {
		return err
	}
	if c == nil {
		return errors.New("nil connector")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	sentinel := errors.New("conformance consumer sentinel")
	var calls atomic.Int32
	_, err := c.Scan(ctx, func(connector.Document) error { calls.Add(1); return sentinel })
	if calls.Load() > 1 {
		return fail("callback_continued", "callback_error", 0, int(calls.Load()))
	}
	if calls.Load() == 0 {
		return fail("callback_not_invoked", "callback_error", 0, 0)
	}
	if !errors.Is(err, sentinel) {
		return fail("callback_error_swallowed", "callback_error", 0, int(calls.Load()))
	}
	return nil
}

// Hooks are optional fixture mutations; absent hooks are visibly skipped by Run.
type Hooks struct{ UpdateContent, UpdateMetadata, DeleteOne, Empty, FailLate, Recover, SecondSource func() connector.Connector }

// Run executes bounded mandatory observations and names unconfigured fixture cases as skipped.
func Run(t *testing.T, factory func() connector.Connector, opts Options, hooks *Hooks) {
	t.Helper()
	if err := opts.validate(); err != nil {
		t.Fatal(err)
	}
	if factory == nil {
		t.Fatal("nil connector factory")
	}
	t.Run("stable_inventory", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		defer cancel()
		if err := CheckStableInventory(ctx, factory, opts); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pre_cancellation", func(t *testing.T) {
		if err := CheckPreCancellation(factory()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("callback_error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		defer cancel()
		c := factory()
		docs, _, err := Observe(ctx, c, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(docs) == 0 {
			t.Skip("empty fixture has no callback to reject")
			return
		}
		if err := CheckConsumerIsolation(ctx, c, opts); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancellation_after_callback", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		defer cancel()
		c := factory()
		docs, _, err := Observe(ctx, c, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(docs) == 0 {
			t.Skip("empty fixture has no accepted callback")
			return
		}
		if err := CheckCancellationRecovery(ctx, c, opts); err != nil {
			t.Fatal(err)
		}
	})
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
			c := tc.hook()
			if tc.name == "late_failure" {
				docs, _, err := Observe(ctx, c, opts)
				if err == nil {
					t.Fatal(fail("incomplete_success", tc.name, 0, len(docs)))
				}
				if len(docs) == 0 {
					t.Fatal(fail("failure_before_callback", tc.name, 0, 0))
				}
				return
			}
			got, _, err := Observe(ctx, c, opts)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "second_source" {
				if err := CheckSourceIsolation(ctx, factory(), c, opts); err != nil {
					t.Fatal(err)
				}
				return
			}
			if tc.name == "recovery" || tc.name == "inventory" {
				return
			}
			before, _, err := Observe(ctx, factory(), opts)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "content_update":
				if !hasTransition(before, got, CheckContentHashChange) {
					t.Fatal(fail("content_update_not_observed", tc.name, 0, len(got)))
				}
			case "metadata_update":
				if !hasTransition(before, got, CheckMetadataObservable) {
					t.Fatal(fail("metadata_update_not_observed", tc.name, 0, len(got)))
				}
			case "single_deletion":
				if len(before) == 0 || len(got) != len(before)-1 {
					t.Fatal(fail("single_deletion_mismatch", tc.name, 0, len(got)))
				}
				for _, d := range got {
					if findByID(before, d.ID) == nil {
						t.Fatal(fail("unexpected_document", tc.name, 0, len(got)))
					}
				}
			case "remove_all":
				if len(before) == 0 || len(got) != 0 {
					t.Fatal(fail("remove_all_mismatch", tc.name, 0, len(got)))
				}
			}
		})
	}
}

type transitionCheck func(connector.Document, connector.Document) error

func hasTransition(before, after []connector.Document, check transitionCheck) bool {
	for _, old := range before {
		if current := findByID(after, old.ID); current != nil && check(old, *current) == nil {
			return true
		}
	}
	return false
}
func findByID(docs []connector.Document, id string) *connector.Document {
	for i := range docs {
		if docs[i].ID == id {
			return &docs[i]
		}
	}
	return nil
}
func hookValue(h *Hooks, f func(*Hooks) func() connector.Connector) func() connector.Connector {
	if h == nil {
		return nil
	}
	return f(h)
}
