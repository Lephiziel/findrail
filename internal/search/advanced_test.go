package search

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAdvanced(t *testing.T) {
	valid := []string{`retry budget`, `"retry budget"`, `retry OR backoff`, `retry budget OR backoff`, `retry -deprecated`, `retry -"old policy"`, `idempot*`, `retry -deprecat*`, `retry -legacy OR backoff`, `lowercase or`, `retry"old policy"OR`}
	for _, q := range valid {
		t.Run(q, func(t *testing.T) {
			if _, err := ParseAdvanced(q); err != nil {
				t.Fatalf("ParseAdvanced(%q): %v", q, err)
			}
		})
	}
	invalid := []string{"OR retry", "retry OR", "-retry", "retry OR -legacy", "--retry", "- retry", "*", "a*", "re*try", "retry**", `"` + "retry" + `"*`, `retry AND budget`, `retry (budget)`, `"retry\q"`, `"!!!"`, `"retry\nbudget"`, "\"retry\nbudget\""}
	for _, q := range invalid {
		t.Run(q, func(t *testing.T) {
			if _, err := ParseAdvanced(q); err == nil {
				t.Fatalf("ParseAdvanced(%q) unexpectedly succeeded", q)
			}
		})
	}
}

func TestValidateFilters(t *testing.T) {
	for _, path := range []string{"docs", "docs/", "docs/a.md", "my docs/notes_%\".md", "資料/手順"} {
		if err := ValidateRequest(Request{Query: "x", PathPrefix: path}); err != nil {
			t.Errorf("valid path %q: %v", path, err)
		}
	}
	for _, path := range []string{"/docs", "C:/docs", `C:\docs`, `a\b`, "a/../b", "a//b", "./a"} {
		if err := ValidateRequest(Request{Query: "x", PathPrefix: path}); err == nil {
			t.Errorf("invalid path %q accepted", path)
		}
	}
}

func TestRequestValidationCodes(t *testing.T) {
	for _, tc := range []struct {
		request Request
		code    string
	}{{Request{Query: "x", Mode: "surprise"}, "invalid_mode"}, {Request{Query: "x", Format: "archive"}, "invalid_filter"}, {Request{Query: "x", TitleContains: "bad\x00title"}, "invalid_filter"}} {
		err := ValidateRequest(tc.request)
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Code != tc.code {
			t.Errorf("ValidateRequest(%+v) error=%v want %q", tc.request, err, tc.code)
		}
	}
}

func FuzzParseAdvanced(f *testing.F) {
	for _, q := range []string{"retry", "\"phrase\" OR prefix*", "-", "\x00", "OR OR"} {
		f.Add(q)
	}
	f.Fuzz(func(t *testing.T, q string) { _, _ = ParseAdvanced(q) })
}

func TestAdvancedTypedErrorsAndBounds(t *testing.T) {
	for _, tc := range []struct{ query, code string }{{"-retry", "missing_positive_term"}, {"retry OR -old", "missing_positive_term"}, {"retry AND budget", "syntax"}, {"(retry)", "syntax"}, {"\x00", "syntax"}, {"\u0301abc", "syntax"}, {strings.Repeat("x", 257), "query_too_large"}} {
		_, err := ParseAdvanced(tc.query)
		var validation *ValidationError
		if err == nil || !errors.As(err, &validation) || validation.Code != tc.code {
			t.Errorf("ParseAdvanced(%q) error=%#v want code %q", tc.query, err, tc.code)
		}
	}
	if _, err := ParseAdvanced("retry " + strings.Repeat("x ", 32)); err == nil {
		t.Fatal("accepted more than 32 atoms")
	}
	if _, err := ParseAdvanced("a OR b OR c OR d OR e OR f OR g OR h OR i"); err == nil {
		t.Fatal("accepted more than 8 branches")
	}
}
