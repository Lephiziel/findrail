package conformance

import (
	"context"
	"errors"
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

func (f *fixture) Source() connector.Source {
	f.sourceCalls++
	if f.unstable && f.sourceCalls > 1 {
		f.src.ID = "changed"
	}
	return f.src
}
func (f *fixture) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	for _, d := range f.docs {
		if err := emit(d); err != nil {
			return f.report, err
		}
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
