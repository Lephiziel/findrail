package manualevidence

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func testContext(candidate map[string]any, native map[string]string) CandidateContext {
	context, err := NewCandidateContext(candidate, native)
	if err != nil {
		panic(err)
	}
	return context
}

func fixture() (map[string]any, map[string]any, map[string]string) {
	sha := strings.Repeat("a", 40)
	version := "0.1.0-beta.1"
	cases := make([]any, len(caseNames))
	for i, name := range caseNames {
		cases[i] = map[string]any{"case": name, "status": "passed", "elapsed_seconds": float64(0)}
	}
	r := map[string]any{"kind": "windows_first_run", "reference": "sha256:" + strings.Repeat("b", 64), "candidate_sha": sha, "candidate_version": version, "target": "windows-amd64", "archive_sha256": strings.Repeat("c", 64), "evidence_type": "manual", "status": "passed", "cases": cases}
	c := map[string]any{"candidate_sha": sha, "candidate_version": version, "schema_version": 8, "client_api_version": 1, "assets": []any{}}
	return r, c, map[string]string{"windows-amd64": strings.Repeat("c", 64)}
}

func TestParseStrictJSON(t *testing.T) {
	for _, raw := range []string{`{"x":1,"x":2}`, `{"a":{"x":1,"x":2}}`, `{"x":NaN}`, `{"x":1e999}`, `{} {}`, `[1]`, strings.Repeat(" ", MaxJSON) + `{}`, strings.Repeat("[", 130) + strings.Repeat("]", 130)} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted invalid JSON: %.20s", raw)
		}
	}
	if _, err := Parse([]byte(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFirstRunAndTypeEdges(t *testing.T) {
	r, c, n := fixture()
	out, err := Validate(r, testContext(c, n))
	if err != nil || out != "complete" {
		t.Fatalf("outcome=%s err=%v", out, err)
	}
	cases := r["cases"].([]any)
	cases[0].(map[string]any)["elapsed_seconds"] = nil
	out, err = Validate(r, testContext(c, n))
	if err != nil || out != "incomplete" {
		t.Fatalf("nullable time: %s %v", out, err)
	}
	cases[0].(map[string]any)["elapsed_seconds"] = true
	if _, err = Validate(r, testContext(c, n)); err == nil {
		t.Fatal("bool accepted as duration")
	}
	cases[0].(map[string]any)["elapsed_seconds"] = -1.0
	if _, err = Validate(r, testContext(c, n)); err == nil {
		t.Fatal("negative duration accepted")
	}
}

func TestValidateUsagePreservesNegativeOutcomes(t *testing.T) {
	_, c, _ := fixture()
	r := map[string]any{"kind": "voluntary_usage", "reference": "sha256:" + strings.Repeat("b", 64), "candidate_sha": c["candidate_sha"], "candidate_version": c["candidate_version"], "evidence_type": "manual", "return_use": true, "retrieval_one": "not_found", "retrieval_two": "found", "collection_restart": "not_restored"}
	out, err := Validate(r, testContext(c, map[string]string{}))
	if err != nil || out != "complete" {
		t.Fatalf("negative observations lost: %s %v", out, err)
	}
	r["return_use"] = false
	out, err = Validate(r, testContext(c, map[string]string{}))
	if err != nil || out != "incomplete" {
		t.Fatalf("return use promoted: %s %v", out, err)
	}
	r["return_use"] = true
	r["retrieval_one"] = 1
	if _, err = Validate(r, testContext(c, map[string]string{})); err == nil {
		t.Fatal("invalid status type accepted")
	}
}

func TestStatusAndAliases(t *testing.T) {
	r, c, n := fixture()
	r["status"] = "failed"
	if o, e := Validate(r, testContext(c, n)); e != nil || o != "failed" {
		t.Fatalf("%s %v", o, e)
	}
	r, c, n = fixture()
	cs := r["cases"].([]any)
	cc := cs[0].(map[string]any)
	cc["id"] = cc["case"]
	delete(cc, "case")
	if o, e := Validate(r, testContext(c, n)); e != nil || o != "complete" {
		t.Fatalf("id alias: %s %v", o, e)
	}
	cc["case"] = cc["id"]
	if _, e := Validate(r, testContext(c, n)); e == nil {
		t.Fatal("both aliases accepted")
	}
}

func TestParseRejectsInvalidUTF8AndTrailingWhitespaceOnly(t *testing.T) {
	if _, err := Parse([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := Parse([]byte("{} \n\t")); err != nil {
		t.Fatalf("valid trailing whitespace rejected: %v", err)
	}
}

func TestValidateRejectsUnknownFieldsBadAliasesAndReferences(t *testing.T) {
	r, candidate, native := fixture()
	r["private_path"] = "/private"
	if _, err := Validate(r, testContext(candidate, native)); err == nil {
		t.Fatal("unknown field accepted")
	}
	delete(r, "private_path")
	cases := r["cases"].([]any)
	cases[0].(map[string]any)["id"] = cases[0].(map[string]any)["case"]
	if _, err := Validate(r, testContext(candidate, native)); err == nil {
		t.Fatal("case and id alias accepted together")
	}
	delete(cases[0].(map[string]any), "id")
	r["reference"] = "sha256:" + strings.Repeat("A", 64)
	if _, err := Validate(r, testContext(candidate, native)); err == nil {
		t.Fatal("uppercase hash reference accepted")
	}
}

func TestValidateOptionalCandidateIdentityRequiresExactInteger(t *testing.T) {
	r, candidate, native := fixture()
	r["schema_version"] = 8.0
	if _, err := Validate(r, testContext(candidate, native)); err == nil {
		t.Fatal("float schema version accepted")
	}
	r["schema_version"] = 8
	candidate["assets"] = []any{
		map[string]any{"basename": "release-manifest.json", "sha256": strings.Repeat("d", 64)},
		map[string]any{"basename": "SHA256SUMS.txt", "sha256": strings.Repeat("e", 64)},
	}
	r["manifest_sha256"] = strings.Repeat("0", 64)
	if _, err := Validate(r, testContext(candidate, native)); err == nil {
		t.Fatal("manifest mismatch accepted")
	}
	r, candidate, native = fixture()
	candidate["assets"] = []any{
		map[string]any{"basename": "release-manifest.json", "sha256": strings.Repeat("d", 64)},
		map[string]any{"basename": "SHA256SUMS.txt", "sha256": strings.Repeat("e", 64)},
	}
	ctx := testContext(candidate, native)
	for field, bad := range map[string]any{"candidate_sha": strings.Repeat("0", 40),
		"candidate_version": "other", "target": "darwin-arm64",
		"archive_sha256": strings.Repeat("0", 64), "schema_version": 7,
		"client_api_version": 2, "manifest_sha256": strings.Repeat("0", 64),
		"checksums_sha256": strings.Repeat("0", 64)} {
		copyReport := make(map[string]any, len(r)+1)
		for key, value := range r {
			copyReport[key] = value
		}
		copyReport[field] = bad
		if _, err := Validate(copyReport, ctx); err == nil {
			t.Errorf("%s mismatch accepted", field)
		}
	}
	rCases := r["cases"].([]any)
	rCases[0], rCases[1] = rCases[1], rCases[0]
	if _, err := Validate(r, ctx); err == nil {
		t.Fatal("case order mismatch accepted")
	}
}

func TestValidateAllFirstRunKindsAndLinuxBlankException(t *testing.T) {
	base, candidate, windowsNative := fixture()
	for kind, target := range map[string]string{
		"windows_first_run":       "windows-amd64",
		"apple_silicon_first_run": "darwin-arm64",
		"linux_first_run":         "linux-amd64",
	} {
		r := map[string]any{}
		for key, value := range base {
			r[key] = value
		}
		r["kind"], r["target"] = kind, target
		hash := strings.Repeat("d", 64)
		native := map[string]string{target: hash}
		r["archive_sha256"] = hash
		if kind == "linux_first_run" {
			r["reference"], r["status"] = "", "not_run"
			cases := make([]any, len(caseNames))
			for i, name := range caseNames {
				cases[i] = map[string]any{"id": name, "status": "not_run", "elapsed_seconds": nil}
			}
			r["cases"] = cases
			if got, err := Validate(r, testContext(candidate, native)); err != nil || got != Incomplete {
				t.Fatalf("Linux blank kit = %q, %v", got, err)
			}
			r["status"] = "passed"
			if _, err := Validate(r, testContext(candidate, native)); err == nil {
				t.Fatal("empty Linux reference accepted for passed report")
			}
			r["reference"] = "sha256:" + strings.Repeat("e", 64)
			for _, value := range r["cases"].([]any) {
				c := value.(map[string]any)
				c["status"], c["elapsed_seconds"] = "passed", 0
			}
			if got, err := Validate(r, testContext(candidate, native)); err != nil || got != Complete {
				t.Fatalf("Linux complete = %q, %v", got, err)
			}
			continue
		}
		if kind == "windows_first_run" {
			native = windowsNative
		}
		r["archive_sha256"] = native[target]
		if got, err := Validate(r, testContext(candidate, native)); err != nil || got != Complete {
			t.Fatalf("%s = %q, %v", kind, got, err)
		}
	}
}

func TestLinuxEmptyReferenceExceptionRequiresPresentString(t *testing.T) {
	for _, status := range []string{"not_run", "incomplete"} {
		for _, value := range []any{nil, 42, true, map[string]any{}} {
			t.Run(status+"/"+fmt.Sprintf("%T", value), func(t *testing.T) {
				r, candidate, _ := fixture()
				hash := strings.Repeat("c", 64)
				r["kind"], r["target"], r["status"] = "linux_first_run", "linux-amd64", status
				r["archive_sha256"], r["reference"] = hash, value
				ctx := testContext(candidate, map[string]string{"linux-amd64": hash})
				if outcome, err := Validate(r, ctx); err == nil {
					t.Fatalf("reference type %T accepted as %s", value, outcome)
				}
			})
		}
		t.Run(status+"/missing", func(t *testing.T) {
			r, candidate, _ := fixture()
			hash := strings.Repeat("c", 64)
			r["kind"], r["target"], r["status"] = "linux_first_run", "linux-amd64", status
			r["archive_sha256"] = hash
			delete(r, "reference")
			ctx := testContext(candidate, map[string]string{"linux-amd64": hash})
			if outcome, err := Validate(r, ctx); err == nil {
				t.Fatalf("missing reference accepted as %s", outcome)
			}
		})
	}
	for _, status := range []string{"not_run", "incomplete"} {
		r, candidate, _ := fixture()
		hash := strings.Repeat("c", 64)
		r["kind"], r["target"], r["status"] = "linux_first_run", "linux-amd64", status
		r["archive_sha256"], r["reference"] = hash, ""
		if outcome, err := Validate(r, testContext(candidate, map[string]string{"linux-amd64": hash})); err != nil || outcome != Incomplete {
			t.Fatalf("explicit empty Linux reference should remain incomplete: %q, %v", outcome, err)
		}
	}
}

func TestParsePreservesFiniteUnderflowAsZero(t *testing.T) {
	value, err := Parse([]byte(`{"elapsed":1e-9999}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := number(value["elapsed"]); err != nil || got != 0 {
		t.Fatalf("underflow: %v %v", got, err)
	}
}

func TestParserAcceptsExactOneMiBBound(t *testing.T) {
	raw := append([]byte(`{}`), bytes.Repeat([]byte{' '}, MaxJSON-2)...)
	if len(raw) != MaxJSON {
		t.Fatalf("fixture length = %d", len(raw))
	}
	if _, err := Parse(raw); err != nil {
		t.Fatalf("exact parser limit rejected: %v", err)
	}
	if _, err := Parse(append(raw, ' ')); err == nil {
		t.Fatal("parser accepted more than 1 MiB")
	}
}
