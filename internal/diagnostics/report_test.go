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
	_, e, o, _ := b.Finish()
	if len(e) != 0 || o != 1 {
		t.Fatalf("unsafe sample retained: %v %d", e, o)
	}
}

func TestBuilderReportSerializationAndCounterOverflowBounds(t *testing.T) {
	b := NewBuilder()
	long := strings.Repeat("界", 600)
	for i := 0; i < MaxExamples+10; i++ {
		b.Add("unsupported_format", "file", long, false)
	}
	reasons, examples, omitted, _ := b.Finish()
	r := Report{FormatVersion: FormatVersion, ID: "id", SourceID: "source", Committed: true, Complete: true, Reasons: reasons, Examples: examples, ExamplesOmitted: omitted}
	if err := r.Validate(); err != nil {
		t.Fatalf("bounded report invalid: %v", err)
	}
	b.Reasons["unsupported_format"] = Reason{Code: "unsupported_format", Unit: "file", Count: int64(^uint64(0) >> 1)}
	b.Add("unsupported_format", "file", "ok.txt", false)
	if b.Reasons["unsupported_format"].Count != int64(^uint64(0)>>1) {
		t.Fatal("reason counter overflowed")
	}
}
