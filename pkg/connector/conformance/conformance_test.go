package conformance

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/pkg/connector"
)

type fixture struct {
	src         connector.Source
	docs        []connector.Document
	report      connector.Report
	err         error
	sourceCalls int
	unstable    bool
}

type unstableID struct{ calls int }

func (f *unstableID) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "test"}
}
func (f *unstableID) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	f.calls++
	d := validDoc()
	d.ID = fmt.Sprintf("id-%d", f.calls)
	if err := emit(d); err != nil {
		return connector.Report{}, err
	}
	return connector.Report{Seen: 1}, nil
}

type pageMutator struct{}

func (pageMutator) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "test"}
}

type callbackMutant struct{ mode string }

func (callbackMutant) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (f callbackMutant) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	d := validDoc()
	err := emit(d)
	if f.mode == "continued" {
		_ = emit(d)
		return connector.Report{Seen: 1}, err
	}
	return connector.Report{Seen: 1}, nil
}

type ignoreCancellation struct{}

func (ignoreCancellation) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (ignoreCancellation) Scan(_ context.Context, emit func(connector.Document) error) (connector.Report, error) {
	d := validDoc()
	if err := emit(d); err != nil {
		return connector.Report{}, err
	}
	_ = emit(d)
	return connector.Report{Seen: 2}, nil
}

type ignorePreCancellation struct{}

func (ignorePreCancellation) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (ignorePreCancellation) Scan(context.Context, func(connector.Document) error) (connector.Report, error) {
	return connector.Report{}, nil
}

type poisonedAfterCancel struct{ poisoned bool }

func (*poisonedAfterCancel) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (f *poisonedAfterCancel) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	if f.poisoned {
		return connector.Report{}, errors.New("fixture poisoned")
	}
	if err := emit(validDoc()); err != nil {
		return connector.Report{}, err
	}
	if err := ctx.Err(); err != nil {
		f.poisoned = true
		return connector.Report{}, err
	}
	return connector.Report{Seen: 1}, nil
}

type poisonedAfterConsumer struct{ poisoned bool }

func (*poisonedAfterConsumer) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (f *poisonedAfterConsumer) Scan(_ context.Context, emit func(connector.Document) error) (connector.Report, error) {
	if f.poisoned {
		return connector.Report{}, errors.New("fixture poisoned")
	}
	if err := emit(validDoc()); err != nil {
		f.poisoned = true
		return connector.Report{}, err
	}
	return connector.Report{Seen: 1}, nil
}

type emptyMutant struct{}

func (emptyMutant) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (emptyMutant) Scan(context.Context, func(connector.Document) error) (connector.Report, error) {
	return connector.Report{}, nil
}

type swallowHarnessFailure struct{}

func (swallowHarnessFailure) Source() connector.Source {
	return connector.Source{ID: "source", Kind: "test", Name: "fixture"}
}
func (swallowHarnessFailure) Scan(_ context.Context, emit func(connector.Document) error) (connector.Report, error) {
	d := validDoc()
	d.SourceID = "wrong"
	_ = emit(d)
	return connector.Report{}, nil
}

type sourceFixture struct{ src connector.Source }

func (f sourceFixture) Source() connector.Source { return f.src }
func (f sourceFixture) Scan(_ context.Context, emit func(connector.Document) error) (connector.Report, error) {
	d := validDoc()
	d.SourceID = f.src.ID
	if err := emit(d); err != nil {
		return connector.Report{}, err
	}
	return connector.Report{Seen: 1}, nil
}
func (pageMutator) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	d := validDoc()
	d.MediaType = "application/pdf"
	d.Pages = []connector.Page{{Number: 1, Text: "before"}}
	if err := emit(d); err != nil {
		return connector.Report{}, err
	}
	d.Pages[0].Text = "after"
	return connector.Report{Seen: 1}, nil
}

func (f *fixture) Source() connector.Source {
	f.sourceCalls++
	if f.unstable && f.sourceCalls > 1 {
		f.src.ID = "changed"
	}
	return f.src
}
func (f *fixture) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	for _, d := range f.docs {
		if err := ctx.Err(); err != nil {
			return f.report, err
		}
		if err := emit(d); err != nil {
			return f.report, err
		}
	}
	if err := ctx.Err(); err != nil {
		return f.report, err
	}
	return f.report, f.err
}
func validDoc() connector.Document {
	return connector.Document{ID: "id", SourceID: "source", Title: "title", URI: "https://example.test/x", Path: "x", Content: "text", Hash: "h", SizeBytes: 4, MediaType: "text/plain"}
}
func base() *fixture {
	d := validDoc()
	return &fixture{src: connector.Source{ID: "source", Kind: "test", Name: "test"}, docs: []connector.Document{d}, report: connector.Report{Seen: 1}}
}
func wantCode(t *testing.T, f *fixture, want string) {
	t.Helper()
	_, _, err := Observe(context.Background(), f, DefaultOptions())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != want {
		t.Fatalf("want code %s; got %#v", want, err)
	}
}
func TestRejectsObservableMutants(t *testing.T) {
	t.Run("unstable source", func(t *testing.T) { f := base(); f.unstable = true; wantCode(t, f, "source_unstable") })
	t.Run("wrong source", func(t *testing.T) { f := base(); f.docs[0].SourceID = "other"; wantCode(t, f, "document_identity") })
	t.Run("duplicate ID", func(t *testing.T) {
		f := base()
		f.docs = append(f.docs, f.docs[0])
		f.report.Seen = 2
		wantCode(t, f, "document_identity")
	})
	t.Run("invalid UTF8", func(t *testing.T) {
		f := base()
		f.docs[0].Title = string([]byte{0xff})
		wantCode(t, f, "invalid_utf8")
	})
	t.Run("missing hash", func(t *testing.T) { f := base(); f.docs[0].Hash = ""; wantCode(t, f, "missing_hash") })
	t.Run("missing provenance", func(t *testing.T) { f := base(); f.docs[0].URI = ""; wantCode(t, f, "missing_provenance") })
	t.Run("wrong Seen", func(t *testing.T) { f := base(); f.report.Seen = 0; wantCode(t, f, "seen_mismatch") })
	t.Run("negative counter", func(t *testing.T) { f := base(); f.report.Skipped = -1; wantCode(t, f, "invalid_report") })
	t.Run("invalid PDF pages", func(t *testing.T) {
		f := base()
		f.docs[0].MediaType = "application/pdf"
		f.docs[0].Pages = []connector.Page{{Number: 0, Text: "page"}}
		wantCode(t, f, "page_sequence")
	})
}

func TestPreCancelledContext(t *testing.T) {
	f := base()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Observe(ctx, f, DefaultOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation")
	}
}
func TestRejectsUnboundedOptions(t *testing.T) {
	o := DefaultOptions()
	o.Limits.Documents = 0
	if _, _, err := Observe(context.Background(), base(), o); err == nil {
		t.Fatal("zero limit accepted")
	}
	o = DefaultOptions()
	o.Timeout = time.Duration(0)
	if _, _, err := Observe(context.Background(), base(), o); err == nil {
		t.Fatal("zero timeout accepted")
	}
	o = DefaultOptions()
	o.Limits.Documents = int(^uint(0) >> 1)
	if _, _, err := Observe(context.Background(), base(), o); err == nil {
		t.Fatal("overflowing/unbounded document limit accepted")
	}
	o = DefaultOptions()
	o.Limits.ContentTotal = -1
	if _, _, err := Observe(context.Background(), base(), o); err == nil {
		t.Fatal("negative capture budget accepted")
	}
	o = DefaultOptions()
	o.Timeout = 31 * time.Second
	if _, _, err := Observe(context.Background(), base(), o); err == nil {
		t.Fatal("timeout beyond profile cap accepted")
	}
}
func TestScanErrorIsSafeAndUnwraps(t *testing.T) {
	sentinel := errors.New("sensitive body")
	f := base()
	f.err = sentinel
	_, _, err := Observe(context.Background(), f, DefaultOptions())
	if !errors.Is(err, sentinel) {
		t.Fatal("cause not unwrapped")
	}
	if err.Error() == sentinel.Error() {
		t.Fatal("sensitive error rendered")
	}
}

func TestFailedScanCountersRemainBoundedAndCauseWrapped(t *testing.T) {
	sentinel := errors.New("opaque")
	f := base()
	f.err = sentinel
	f.report.Skipped = -1
	_, _, err := Observe(context.Background(), f, DefaultOptions())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "invalid_report" || !errors.Is(err, sentinel) {
		t.Fatalf("bad failed-scan diagnostics: %v", err)
	}
}

func TestStableInventoryRejectsUnstableIDs(t *testing.T) {
	f := &unstableID{}
	err := CheckStableInventory(context.Background(), func() connector.Connector { return f }, DefaultOptions())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "inventory_unstable" {
		t.Fatalf("expected inventory_unstable, got %v", err)
	}
}
func TestPagesAreDeepCopiedAtCallbackBoundary(t *testing.T) {
	docs, _, err := Observe(context.Background(), pageMutator{}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Pages[0].Text != "before" {
		t.Fatal("observed page slice was mutated after callback")
	}
}
func TestCallbackSentinelChecks(t *testing.T) {
	if err := CheckCallbackError(context.Background(), base(), DefaultOptions()); err != nil {
		t.Fatalf("fixture should preserve callback errors: %v", err)
	}
	if err := CheckPreCancellation(base()); err != nil {
		t.Fatalf("fixture should respect pre-cancellation: %v", err)
	}
}

func TestHashAndMetadataMutants(t *testing.T) {
	before := validDoc()
	after := before
	after.Content = "different"
	if err := CheckContentHashChange(before, after); err == nil {
		t.Fatal("stale content hash accepted")
	}
	before = validDoc()
	before.MediaType = "application/pdf"
	before.Pages = []connector.Page{{Number: 1, Text: "ab"}, {Number: 2, Text: "cd"}}
	after = before
	after.Pages = []connector.Page{{Number: 1, Text: "a"}, {Number: 2, Text: "bcd"}}
	if err := CheckContentHashChange(before, after); err == nil {
		t.Fatal("changed PDF page boundary with old hash accepted")
	}
	after = before
	after.Title = "renamed"
	after.Hash = "metadata-inclusive-hash"
	if err := CheckMetadataObservable(before, after); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryChecksRejectPoisonedAdapters(t *testing.T) {
	var failure *Failure
	err := CheckCancellationRecovery(context.Background(), &poisonedAfterCancel{}, DefaultOptions())
	if !errors.As(err, &failure) || failure.Code != "recovery_failed" {
		t.Fatalf("expected cancellation recovery failure, got %v", err)
	}
	err = CheckConsumerIsolation(context.Background(), &poisonedAfterConsumer{}, DefaultOptions())
	if !errors.As(err, &failure) || failure.Code != "consumer_state_leaked" {
		t.Fatalf("expected consumer state leak, got %v", err)
	}
}

func TestSourceIsolationRejectsCollisions(t *testing.T) {
	a, b := base(), base()
	err := CheckSourceIsolation(context.Background(), a, b, DefaultOptions())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "source_collision" {
		t.Fatalf("expected source collision, got %v", err)
	}
}

func TestRequiredFailureMutants(t *testing.T) {
	for mode, want := range map[string]string{"swallowed": "callback_error_swallowed", "continued": "callback_continued"} {
		err := CheckCallbackError(context.Background(), callbackMutant{mode}, DefaultOptions())
		var failure *Failure
		if !errors.As(err, &failure) || failure.Code != want {
			t.Errorf("mode %s: expected %s, got %v", mode, want, err)
		}
	}
	err := CheckExpectedInventory(context.Background(), emptyMutant{}, []connector.Document{validDoc()}, DefaultOptions())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "incomplete_inventory" {
		t.Fatalf("expected incomplete inventory, got %v", err)
	}
	_, _, err = Observe(context.Background(), swallowHarnessFailure{}, DefaultOptions())
	if !errors.As(err, &failure) || failure.Code != "document_identity" {
		t.Fatalf("connector swallowed harness callback error: %v", err)
	}
	a := sourceFixture{connector.Source{ID: "a", Kind: "test", Name: "a"}}
	b := sourceFixture{connector.Source{ID: "b", Kind: "test", Name: "b"}}
	err = CheckSourceIsolation(context.Background(), a, b, DefaultOptions())
	if !errors.As(err, &failure) || failure.Code != "document_collision" {
		t.Fatalf("expected cross-source ID collision, got %v", err)
	}
	err = CheckCancellationAfterFirst(context.Background(), ignoreCancellation{}, DefaultOptions())
	if !errors.As(err, &failure) || failure.Code != "cancel_continued" {
		t.Fatalf("expected cancellation mutant rejection, got %v", err)
	}
	err = CheckPreCancellation(ignorePreCancellation{})
	if !errors.As(err, &failure) || failure.Code != "cancel_ignored" {
		t.Fatalf("expected pre-cancellation mutant rejection, got %v", err)
	}
}

func ExampleObserve() {
	_, _, err := Observe(context.Background(), base(), DefaultOptions())
	fmt.Println(err == nil) // Output: true
}

func TestExampleRunner(t *testing.T) {
	Run(t, func() connector.Connector { return base() }, DefaultOptions(), nil)
}
