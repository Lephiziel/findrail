// findrail-manual is the offline maintainer interface for manual evidence.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/Lephiziel/findrail/internal/devtools/manualevidence"
)

const maxCLIOutput = 8 << 20
const maxReferenceOutput = 4 << 20

type contextResult struct {
	ProtocolVersion int                             `json:"protocol_version"`
	Candidate       map[string]any                  `json:"candidate"`
	Native          map[string]string               `json:"native_archives"`
	Validation      manualevidence.CandidateContext `json:"-"`
}
type counts struct {
	Received   int `json:"received"`
	Complete   int `json:"complete"`
	Incomplete int `json:"incomplete"`
	Failed     int `json:"failed"`
	Mismatch   int `json:"mismatch"`
}
type summary struct {
	Status          string   `json:"status"`
	Counts          counts   `json:"counts"`
	Issues          []string `json:"issues"`
	References      []string `json:"references"`
	Outputs         []string `json:"outputs"`
	ReleaseDecision bool     `json:"release_decision"`
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("oversize")
	}
	return b, nil
}
func strictContext(python, readiness string, raw []byte) (contextResult, error) {
	var out contextResult
	f, e := os.CreateTemp("", "findrail-manual-candidate-*.json")
	if e != nil {
		return out, e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return out, e
	}
	if e = f.Close(); e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, readiness, "--manual-context", "--candidate", name)
	var stdout limitedBuffer
	stdout.limit = 8 << 20
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil || stdout.overflow {
		return out, errors.New("candidate_invalid")
	}
	d := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	d.UseNumber()
	d.DisallowUnknownFields()
	if e = d.Decode(&out); e != nil || out.ProtocolVersion != 1 || out.Candidate == nil || out.Native == nil {
		return out, errors.New("candidate_invalid")
	}
	if d.Decode(new(any)) != io.EOF {
		return out, errors.New("candidate_invalid")
	}
	out.Validation, e = manualevidence.NewCandidateContext(out.Candidate, out.Native)
	if e != nil {
		return out, errors.New("candidate_invalid")
	}
	return out, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.overflow = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
func asMap(v any) map[string]any            { m, _ := v.(map[string]any); return m }
func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func safeCases(report map[string]any) []map[string]any {
	in, _ := report["cases"].([]any)
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		c := asMap(item)
		name := str(c, "case")
		if name == "" {
			name = str(c, "id")
		}
		out = append(out, map[string]any{"case": name, "status": c["status"], "elapsed_seconds": c["elapsed_seconds"]})
	}
	return out
}
func writeJSON(path string, v any) error {
	data, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if len(data)+1 > maxCLIOutput {
		return errors.New("output too large")
	}
	data = append(data, '\n')
	tmp, e := os.CreateTemp(filepath.Dir(path), ".manual-*.tmp")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, e = tmp.Write(data); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return os.Rename(name, path)
}
func identityMismatch(r map[string]any, candidate manualevidence.CandidateContext) bool {
	if str(r, "candidate_sha") != candidate.CandidateSHA ||
		str(r, "candidate_version") != candidate.CandidateVersion {
		return true
	}
	kind := str(r, "kind")
	target := map[string]string{"windows_first_run": "windows-amd64", "apple_silicon_first_run": "darwin-arm64", "linux_first_run": "linux-amd64"}[kind]
	if target != "" && (r["target"] != target || r["archive_sha256"] != candidate.NativeArchives[target]) {
		return true
	}
	assets := candidate.AssetHashes
	if value, ok := r["schema_version"]; ok && !sameInt(value, candidate.SchemaVersion) {
		return true
	}
	if value, ok := r["client_api_version"]; ok && !sameInt(value, candidate.ClientAPIVersion) {
		return true
	}
	if value, ok := r["manifest_sha256"]; ok && value != assets["release-manifest.json"] {
		return true
	}
	if value, ok := r["checksums_sha256"]; ok && value != assets["SHA256SUMS.txt"] {
		return true
	}
	return false
}
func sameInt(a, b any) bool {
	integer := func(v any) (int64, bool) {
		switch n := v.(type) {
		case json.Number:
			i, e := n.Int64()
			return i, e == nil
		case int:
			return int64(n), true
		case int64:
			return n, true
		default:
			return 0, false
		}
	}
	x, ok := integer(a)
	if !ok {
		return false
	}
	y, ok := integer(b)
	return ok && x == y
}

func run(args []string) int {
	fs := flag.NewFlagSet("intake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	candidate := fs.String("candidate", "", "verified candidate JSON")
	python := fs.String("python", "", "explicit Python executable")
	readiness := fs.String("readiness", "", "readiness.py path")
	output := fs.String("output", "", "output directory")
	var reports stringsFlag
	fs.Var(&reports, "report", "report JSON (repeatable)")
	if fs.Parse(args) != nil || *candidate == "" || *python == "" || *readiness == "" || *output == "" || len(reports) < 1 {
		return 2
	}
	outDir := *output
	if os.MkdirAll(outDir, 0700) != nil {
		return 2
	}
	sum := summary{Status: "blocked", Counts: counts{Received: len(reports)},
		Issues: []string{"intake_interrupted"}, References: []string{}, Outputs: []string{}, ReleaseDecision: false}
	if writeJSON(filepath.Join(outDir, "intake.json"), sum) != nil {
		return 2
	}
	cleanupFailed := false
	for _, name := range []string{"supplemental.json", "linux-observations.json", "usage-observations.json"} {
		err := os.Remove(filepath.Join(outDir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupFailed = true
		}
	}
	if cleanupFailed {
		sum.Counts.Failed = len(reports)
		sum.Issues = []string{"output_cleanup_failed"}
		_ = writeJSON(filepath.Join(outDir, "intake.json"), sum)
		b, _ := json.Marshal(sum)
		fmt.Println(string(b))
		return 1
	}
	sum.Issues = []string{}
	issues := map[string]bool{}
	outputFailed := false
	refs := map[string]bool{}
	kinds := map[string]bool{}
	var records, linux, usage []map[string]any
	linuxFailed, usageFailed := 0, 0
	referenceOutputBytes := 0
	rawCandidate, e := readBounded(*candidate, manualevidence.MaxJSON)
	var ctx contextResult
	if e == nil {
		var parsed map[string]any
		parsed, e = manualevidence.Parse(rawCandidate)
		if e == nil {
			ctx, e = strictContext(*python, *readiness, rawCandidate)
			if e == nil {
				if str(parsed, "candidate_sha") != str(ctx.Candidate, "candidate_sha") || str(parsed, "candidate_version") != str(ctx.Candidate, "candidate_version") {
					e = errors.New("candidate_invalid")
				}
			}
		}
	}
	if e != nil || len(reports) > manualevidence.MaxUsage {
		sum.Counts.Failed = len(reports)
		issues["candidate_invalid"] = true
	} else {
		for _, path := range reports {
			raw, err := readBounded(path, manualevidence.MaxJSON)
			var r map[string]any
			if err == nil {
				r, err = manualevidence.Parse(raw)
			}
			if err != nil {
				sum.Counts.Failed++
				issues["report_invalid"] = true
				continue
			}
			kind := str(r, "kind")
			ref := str(r, "reference")
			if identityMismatch(r, ctx.Validation) {
				sum.Counts.Mismatch++
				issues["candidate_mismatch"] = true
				continue
			}
			outcome, err := manualevidence.Validate(r, ctx.Validation)
			if err != nil {
				sum.Counts.Failed++
				issues["report_invalid"] = true
				continue
			}
			if ref != "" && refs[ref] {
				sum.Counts.Failed++
				issues["duplicate_reference"] = true
				continue
			}
			if ref != "" {
				if referenceOutputBytes+len(ref) > maxReferenceOutput {
					sum.Counts.Failed++
					issues["report_invalid"] = true
					continue
				}
				refs[ref] = true
				referenceOutputBytes += len(ref)
			}
			kinds[kind] = true
			if outcome == "complete" {
				sum.Counts.Complete++
			} else if outcome == "incomplete" {
				sum.Counts.Incomplete++
				issues["report_incomplete"] = true
			} else {
				sum.Counts.Failed++
				issues["report_failed"] = true
				if kind == "linux_first_run" {
					linuxFailed++
				}
				if kind == "voluntary_usage" {
					usageFailed++
				}
			}
			if kind == "linux_first_run" {
				linux = append(linux, map[string]any{"status": outcome, "target": r["target"], "archive_sha256": r["archive_sha256"], "reference": ref, "cases": safeCases(r)})
			}
			if kind == "voluntary_usage" {
				usage = append(usage, map[string]any{"reference": ref, "evidence_type": r["evidence_type"], "return_use": r["return_use"], "retrieval_one": r["retrieval_one"], "retrieval_two": r["retrieval_two"], "collection_restart": r["collection_restart"], "status": outcome})
			}
			if outcome == "complete" && kind != "linux_first_run" {
				if kind == "voluntary_usage" {
					records = append(records, usage[len(usage)-1])
				} else {
					cases := safeCases(r)
					for _, c := range cases {
						c["status"] = "passed"
					}
					records = append(records, map[string]any{"kind": kind, "evidence_type": "manual", "target": r["target"], "archive_sha256": r["archive_sha256"], "reference": ref, "status": "passed", "candidate_sha": ctx.Candidate["candidate_sha"], "candidate_version": ctx.Candidate["candidate_version"], "cases": cases})
				}
			}
		}
	}
	if len(kinds) > 1 {
		issues["mixed_report_kinds"] = true
	}
	ready := e == nil && sum.Counts.Complete > 0 && sum.Counts.Failed == 0 && sum.Counts.Incomplete == 0 && sum.Counts.Mismatch == 0 && len(kinds) == 1 && !kinds["linux_first_run"]
	if ready {
		kind := ""
		for k := range kinds {
			kind = k
		}
		var payload any
		if kind == "voluntary_usage" {
			payload = map[string]any{"kind": kind, "candidate_sha": ctx.Candidate["candidate_sha"], "candidate_version": ctx.Candidate["candidate_version"], "reports": records}
		} else if len(records) > 0 {
			payload = records[0]
		}
		if payload != nil {
			if os.MkdirAll(outDir, 0700) == nil && writeJSON(filepath.Join(outDir, "supplemental.json"), payload) == nil {
				sum.Outputs = append(sum.Outputs, "supplemental.json")
			} else {
				ready = false
				outputFailed = true
				issues["output_error"] = true
			}
		}
	}
	linuxOnly := len(kinds) == 1 && kinds["linux_first_run"] && sum.Counts.Mismatch == 0 &&
		sum.Counts.Failed == linuxFailed && sum.Counts.Complete+sum.Counts.Incomplete+linuxFailed == sum.Counts.Received && len(linux) > 0
	linuxComplete := linuxOnly && sum.Counts.Incomplete == 0 && sum.Counts.Failed == 0 && sum.Counts.Complete > 0
	usageOnly := len(kinds) == 1 && kinds["voluntary_usage"] && !ready && sum.Counts.Mismatch == 0 &&
		sum.Counts.Failed == usageFailed && sum.Counts.Complete+sum.Counts.Incomplete+usageFailed == sum.Counts.Received && len(usage) > 0
	if linuxOnly {
		status := "complete"
		if sum.Counts.Incomplete > 0 {
			status = "incomplete"
		}
		if sum.Counts.Failed > 0 {
			status = "failed"
		}
		payload := map[string]any{"format_version": 1, "kind": "linux_first_run", "candidate_sha": ctx.Candidate["candidate_sha"], "candidate_version": ctx.Candidate["candidate_version"], "observations": linux}
		if writeJSON(filepath.Join(outDir, "linux-observations.json"), payload) == nil {
			sum.Outputs = append(sum.Outputs, "linux-observations.json")
		} else {
			outputFailed = true
			issues["output_error"] = true
		}
		if status == "complete" {
			sum.Status = "recorded"
		} else if status == "incomplete" {
			sum.Status = "incomplete"
		}
	}
	if usageOnly {
		payload := map[string]any{"format_version": 1, "kind": "voluntary_usage", "candidate_sha": ctx.Candidate["candidate_sha"], "candidate_version": ctx.Candidate["candidate_version"], "observations": usage}
		if writeJSON(filepath.Join(outDir, "usage-observations.json"), payload) == nil {
			sum.Outputs = append(sum.Outputs, "usage-observations.json")
		} else {
			outputFailed = true
			issues["output_error"] = true
		}
	}
	if outputFailed {
		sum.Status = "blocked"
	} else if ready {
		sum.Status = "recorded"
	} else if sum.Status != "recorded" && sum.Status != "incomplete" {
		if sum.Counts.Failed > 0 || sum.Counts.Mismatch > 0 || len(kinds) > 1 {
			sum.Status = "blocked"
		} else if sum.Counts.Complete+sum.Counts.Incomplete > 0 {
			sum.Status = "incomplete"
		}
	}
	for i := range issues {
		sum.Issues = append(sum.Issues, i)
	}
	for r := range refs {
		sum.References = append(sum.References, r)
	}
	sort.Strings(sum.Issues)
	sort.Strings(sum.References)
	if os.MkdirAll(outDir, 0700) != nil || writeJSON(filepath.Join(outDir, "intake.json"), sum) != nil {
		return 2
	}
	b, _ := json.Marshal(sum)
	if len(b) > maxCLIOutput {
		return 2
	}
	fmt.Println(string(b))
	if (ready || linuxComplete) && !outputFailed {
		return 0
	}
	return 1
}

type stringsFlag []string

func (s *stringsFlag) String() string     { return fmt.Sprint([]string(*s)) }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }
func validateCommand(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	candidate := fs.String("candidate", "", "candidate summary")
	report := fs.String("report", "", "manual report")
	python := fs.String("python", "", "explicit Python executable")
	readiness := fs.String("readiness", "", "readiness.py path")
	maxBytes := fs.Int64("max-json", manualevidence.MaxJSON, "report byte limit")
	if fs.Parse(args) != nil || *candidate == "" || *report == "" || *python == "" || *readiness == "" || *maxBytes < 1 || *maxBytes > manualevidence.MaxJSON {
		return 2
	}
	cb, e := readBounded(*candidate, manualevidence.MaxJSON)
	if e != nil {
		fmt.Println(`{"error":"candidate_invalid"}`)
		return 1
	}
	rb, e := readBounded(*report, *maxBytes)
	if e != nil {
		fmt.Println(`{"error":"report_invalid"}`)
		return 1
	}
	ctx, e := strictContext(*python, *readiness, cb)
	if e != nil {
		fmt.Println(`{"error":"candidate_invalid"}`)
		return 1
	}
	r, e := manualevidence.Parse(rb)
	if e != nil {
		fmt.Println(`{"error":"report_invalid"}`)
		return 1
	}
	outcome, e := manualevidence.Validate(r, ctx.Validation)
	if e != nil {
		fmt.Println(`{"error":"report_invalid"}`)
		return 1
	}
	cases := safeCases(r)
	for _, c := range cases {
		delete(c, "observation")
		delete(c, "obstacle")
	}
	result := map[string]any{"protocol_version": 1, "outcome": outcome, "kind": r["kind"], "status": r["status"], "reference": r["reference"], "evidence_type": r["evidence_type"], "target": r["target"], "archive_sha256": r["archive_sha256"], "candidate_sha": r["candidate_sha"], "candidate_version": r["candidate_version"], "cases": cases}
	b, _ := json.Marshal(result)
	if len(b) > maxCLIOutput {
		fmt.Println(`{"error":"output_too_large"}`)
		return 1
	}
	fmt.Println(string(b))
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: findrail-manual intake|validate")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "intake":
		os.Exit(run(os.Args[2:]))
	case "validate":
		os.Exit(validateCommand(os.Args[2:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown command")
		os.Exit(2)
	}
}
