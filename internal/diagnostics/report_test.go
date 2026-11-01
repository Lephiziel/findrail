package diagnostics

import (
	"strings"
	"testing"
)

func TestBuilderBoundsSamplesAndRedactsSensitivePaths(t *testing.T) {
	b := NewBuilder()
	b.Add("sensitive_name", "file", "secret-token.txt", true)
	for i := 0; i < MaxExamples+7; i++ {
		b.Add("unsupported_format", "file", "item.txt", false)
	}
	r, e, o, redacted := b.Finish()
	if len(e) != MaxExamples || o != 7 || redacted != 1 || len(r) != 2 {
		t.Fatalf("unexpected bounds: reasons=%v examples=%d omitted=%d redacted=%d", r, len(e), o, redacted)
	}
	for _, x := range e {
		if x.Path == "secret-token.txt" {
			t.Fatal("sensitive path retained")
		}
	}
	if r[1].Count != MaxExamples+7 {
		t.Fatalf("aggregate stopped at sample cap: %+v", r)
	}
}

func TestBuilderRejectsUnsafePathAsExample(t *testing.T) {
	b := NewBuilder()
	b.Add("unsupported_format", "file", "../escape.txt", false)
	b.Add("unsupported_format", "file", "note\u202e.png", false)
	_, e, o, _ := b.Finish()
	if len(e) != 0 || o != 2 {
		t.Fatalf("unsafe sample retained: %v %d", e, o)
	}
}

func TestSameReasonPreservesFileAndDirectoryUnits(t *testing.T) {
	b := NewBuilder()
	b.Add("hidden_entry", "file", ".hidden.md", false)
	b.Add("hidden_entry", "directory", ".private", false)
	reasons, _, _, _ := b.Finish()
	if len(reasons) != 2 || reasons[0].Code != reasons[1].Code || reasons[0].Unit == reasons[1].Unit {
		t.Fatalf("units were merged: %+v", reasons)
	}
}

func TestBuilderReportSerializationAndCounterOverflowBounds(t *testing.T) {
	b := NewBuilder()
	long := strings.Repeat("界", 600)
	for i := 0; i < MaxExamples+10; i++ {
		b.Add("unsupported_format", "file", long, false)
	}
	reasons, examples, omitted, _ := b.Finish()
	r := Report{FormatVersion: FormatVersion, ID: "id", SourceID: "source", Operation: "refresh", Coverage: "complete_filesystem", Committed: true, Complete: true, ObservedFilesKnown: true, ObservedEntriesKnown: true, ObservedDirectoriesKnown: true, Reasons: reasons, Examples: examples, ExamplesOmitted: omitted}
	if err := r.Validate(); err != nil {
		t.Fatalf("bounded report invalid: %v", err)
	}
	key := "unsupported_format\x00file"
	b.Reasons[key] = Reason{Code: "unsupported_format", Unit: "file", Count: int64(^uint64(0) >> 1)}
	b.Add("unsupported_format", "file", "ok.txt", false)
	if b.Reasons[key].Count != int64(^uint64(0)>>1) {
		t.Fatal("reason counter overflowed")
	}
	if !b.Overflow {
		t.Fatal("counter overflow was not reported")
	}
}
