package ignorepolicy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	p, err := Parse(context.Background(), []byte("*.log\n/notes/*\n!/notes/keep.md\nnotes/**/draft-?.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path      string
		dir, want bool
	}{{"a/x.log", false, true}, {"x.log", false, true}, {"notes/keep.md", false, false}, {"notes/a/draft-1.md", false, true}, {"notes/draft-1.md", false, true}} {
		got := p.Decide(context.Background(), tc.path, tc.dir).Excluded
		if got != tc.want {
			t.Errorf("%s excluded=%v want %v", tc.path, got, tc.want)
		}
	}
}
func TestSyntaxAndBounds(t *testing.T) {
	for _, s := range []string{"a**b\n", "../x\n", "[a]\n", "C:/x\n", "a\\b\n", "a//b\n", "./a\n", "scheme://host\n"} {
		if _, err := Parse(context.Background(), []byte(s)); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	if ValidatePath("../secret") == nil {
		t.Fatal("traversal accepted")
	}
}
func TestBOMCommentEscapesAndUnicode(t *testing.T) {
	p, e := Parse(context.Background(), []byte("\xef\xbb\xbf# comment\r\n\\#literal\r\n?/é.txt\r\n"))
	if e != nil {
		t.Fatal(e)
	}
	if !p.Decide(context.Background(), "#literal", false).Excluded || !p.Decide(context.Background(), "x/é.txt", false).Excluded {
		t.Fatal("expected matching rules")
	}
}
func TestDirectoryOnlyPrecedenceCaseAndLiteralBang(t *testing.T) {
	p, err := Parse(context.Background(), []byte("cache/\n*.LOG\n\\!literal.md\nname   \n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path      string
		dir, want bool
	}{{"cache", true, true}, {"cache", false, false}, {"nested/cache", true, true}, {"nested/a.log", false, false}, {"nested/a.LOG", false, true}, {"!literal.md", false, true}, {"name", false, true}} {
		if got := p.Decide(context.Background(), tc.path, tc.dir).Excluded; got != tc.want {
			t.Errorf("%q dir=%v excluded=%v want=%v", tc.path, tc.dir, got, tc.want)
		}
	}
	if _, err := Parse(context.Background(), []byte("# comment\x00\n")); err == nil {
		t.Fatal("control in comment accepted")
	}
}

func TestParseLimitsAndSafeErrors(t *testing.T) {
	if _, err := Parse(context.Background(), []byte(strings.Repeat("#", MaxInput+1))); err == nil || strings.Contains(err.Error(), "#") {
		t.Fatalf("input limit/safe error: %v", err)
	}
	tooMany := strings.Repeat("a\n", MaxRules+1)
	if _, err := Parse(context.Background(), []byte(tooMany)); err == nil {
		t.Fatal("rule limit ignored")
	}
	tooLong := strings.Repeat("a", MaxPatternBytes+1)
	if _, err := Parse(context.Background(), []byte(tooLong)); err == nil {
		t.Fatal("pattern limit ignored")
	}
	tooManySegments := strings.Repeat("a/", MaxPatternSegments) + "a"
	if _, err := Parse(context.Background(), []byte(tooManySegments)); err == nil {
		t.Fatal("pattern segment limit ignored")
	}
	if _, err := Parse(context.Background(), []byte{0xff}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	for _, path := range []string{"C:\\notes\\a.md", "//server/share/a", "scheme://host/path", "a//b", "a/./b", "a/../b"} {
		if ValidatePath(path) == nil {
			t.Errorf("accepted unsafe path %q", path)
		}
	}
}

func TestCancellationDuringPolicyParseAndMatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, []byte("*.md\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("parse cancellation: %v", err)
	}
	p, err := Parse(context.Background(), []byte("*.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Decide(ctx, "note.md", false).Reason; got != "canceled" {
		t.Fatalf("match cancellation: %s", got)
	}
}

func TestCompiledSegmentBudget(t *testing.T) {
	line := strings.Repeat("a/", MaxPatternSegments-1) + "a\n"
	raw := strings.Repeat(line, MaxCompiledSegments/MaxPatternSegments)
	if _, err := Parse(context.Background(), []byte(raw)); err != nil {
		t.Fatalf("Parse() at segment limit: %v", err)
	}
	_, err := Parse(context.Background(), []byte(raw+"a\n"))
	var policyErr *Error
	if !errors.As(err, &policyErr) || policyErr.Code != "compiled_limit" {
		t.Fatalf("Parse() above segment limit: %v, want compiled_limit", err)
	}
}
func FuzzParseAndMatch(f *testing.F) {
	f.Add("*.log\n", "a.log")
	f.Add("/cache/**\n", "cache/a")
	f.Fuzz(func(t *testing.T, raw, path string) {
		if len(raw) > MaxInput || len(path) > MaxPathBytes {
			return
		}
		p, e := Parse(context.Background(), []byte(raw))
		if e == nil {
			_ = p.Decide(context.Background(), path, false)
		} else if len(e.Error()) > 128 {
			t.Fatal("unbounded error")
		}
	})
}

func FuzzValidateRelativePath(f *testing.F) {
	for _, seed := range []string{"notes.md", "a/b/é.md", "../escape", `C:\\secret`, "a//b", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if len(path) > MaxPathBytes+1 {
			return
		}
		err := ValidatePath(path)
		if err == nil {
			p, e := Parse(context.Background(), []byte("*\n"))
			if e != nil {
				t.Fatal(e)
			}
			d := p.Decide(context.Background(), path, false)
			if d.Reason != "eligible" && d.Reason != "user_rule" {
				t.Fatalf("validated path had inconsistent decision: %q %+v", path, d)
			}
		}
	})
}

func BenchmarkNearLimitMatch(b *testing.B) {
	line := strings.Repeat("a*/", MaxPatternSegments-1) + "a*\n"
	raw := strings.Repeat(line, MaxCompiledSegments/MaxPatternSegments)
	p, err := Parse(context.Background(), []byte(raw))
	if err != nil {
		b.Fatal(err)
	}
	path := strings.Repeat("abcdefghijklmno/", MaxPathSegments-1) + "abcdefghijklmno"
	b.SetBytes(int64(len(raw) + len(path)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Decide(context.Background(), path, false)
	}
}
