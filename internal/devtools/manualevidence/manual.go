// Package manualevidence validates and classifies voluntary manual evidence.
// It is a development tool and is not part of the Findrail runtime.
package manualevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxJSON = 1 << 20
const MaxUsage = 100

type Kind string
type Outcome string

const (
	WindowsFirstRun      Kind    = "windows_first_run"
	AppleSiliconFirstRun Kind    = "apple_silicon_first_run"
	LinuxFirstRun        Kind    = "linux_first_run"
	VoluntaryUsage       Kind    = "voluntary_usage"
	Complete             Outcome = "complete"
	Incomplete           Outcome = "incomplete"
	Failed               Outcome = "failed"
)

type CandidateContext struct {
	CandidateSHA     string
	CandidateVersion string
	SchemaVersion    int64
	ClientAPIVersion int64
	AssetHashes      map[string]string
	NativeArchives   map[string]string
}

var caseNames = []string{"install", "launch", "repeat-launch", "retrieval", "collection-restart", "refresh", "quit", "uninstall-data-preserved"}

// CaseNames returns a copy of the required ordered first-run case names.
func CaseNames() []string { return append([]string(nil), caseNames...) }

// NewCandidateContext extracts typed identity from the immutable snapshot that
// readiness.strict_candidate has already validated.
func NewCandidateContext(summary map[string]any, native map[string]string) (CandidateContext, error) {
	sha, shaOK := summary["candidate_sha"].(string)
	version, versionOK := summary["candidate_version"].(string)
	schema, schemaOK := integer(summary["schema_version"])
	api, apiOK := integer(summary["client_api_version"])
	assets, assetsOK := summary["assets"].([]any)
	if !shaOK || !versionOK || !schemaOK || !apiOK || !assetsOK {
		return CandidateContext{}, invalid()
	}
	assetHashes := make(map[string]string, len(assets))
	for _, value := range assets {
		asset, ok := value.(map[string]any)
		if !ok {
			return CandidateContext{}, invalid()
		}
		name, nameOK := asset["basename"].(string)
		hash, hashOK := asset["sha256"].(string)
		if !nameOK || !hashOK {
			return CandidateContext{}, invalid()
		}
		if _, exists := assetHashes[name]; exists {
			return CandidateContext{}, invalid()
		}
		assetHashes[name] = hash
	}
	if native == nil {
		return CandidateContext{}, invalid()
	}
	return CandidateContext{CandidateSHA: sha, CandidateVersion: version, SchemaVersion: schema,
		ClientAPIVersion: api, AssetHashes: assetHashes,
		NativeArchives: map[string]string{"windows-amd64": native["windows-amd64"],
			"darwin-arm64": native["darwin-arm64"], "linux-amd64": native["linux-amd64"]}}, nil
}

var kinds = map[Kind]bool{WindowsFirstRun: true, AppleSiliconFirstRun: true, LinuxFirstRun: true, VoluntaryUsage: true}
var referenceRE = regexp.MustCompile(`^(https://github\.com/Lephiziel/findrail/issues/[1-9][0-9]*|sha256:[0-9a-f]{64})$`)
var targets = map[Kind]string{WindowsFirstRun: "windows-amd64", AppleSiliconFirstRun: "darwin-arm64", LinuxFirstRun: "linux-amd64"}
var common = strings.Fields("kind reference candidate_sha candidate_version target archive_sha256 evidence_type cases return_use retrieval_one retrieval_two collection_restart status obstacle observer date notes schema_version client_api_version manifest_sha256 checksums_sha256")

// Parse accepts one bounded JSON object, rejects duplicate keys/trailing data,
// and preserves exact JSON types for subsequent validation.
func Parse(raw []byte) (map[string]any, error) {
	if len(raw) > MaxJSON || !utf8.Valid(raw) {
		return nil, errors.New("invalid report")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := decodeValue(d, 0)
	if err != nil {
		return nil, errors.New("invalid report")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("invalid report")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("invalid report")
	}
	return m, nil
}

func decodeValue(d *json.Decoder, depth int) (any, error) {
	if depth > 128 {
		return nil, errors.New("depth")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, errors.New("key")
				}
				if _, exists := m[key]; exists {
					return nil, errors.New("duplicate")
				}
				v, e := decodeValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, err = d.Token()
			return m, err
		case '[':
			a := []any{}
			for d.More() {
				v, e := decodeValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, err = d.Token()
			return a, err
		default:
			return nil, errors.New("delimiter")
		}
	case json.Number:
		f, e := x.Float64()
		if (e != nil && f != 0) || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, errors.New("number")
		}
		return x, nil
	default:
		return x, nil
	}
}

func invalid() error           { return errors.New("invalid report") }
func text(v any, max int) bool { s, ok := v.(string); return v == nil || ok && len([]rune(s)) <= max }
func contains(fields []string, key string) bool {
	for _, f := range fields {
		if key == f {
			return true
		}
	}
	return false
}

// Validate returns complete, incomplete, or failed, matching the historical
// manual intake contract. native maps target to its archive sha256.
func Validate(r map[string]any, context CandidateContext) (Outcome, error) {
	for k := range r {
		if !contains(common, k) {
			return "", invalid()
		}
	}
	kindText, _ := r["kind"].(string)
	kind := Kind(kindText)
	if !kinds[kind] {
		return "", invalid()
	}
	if r["candidate_sha"] != context.CandidateSHA || r["candidate_version"] != context.CandidateVersion {
		return "", invalid()
	}
	for field, expected := range map[string]int64{"schema_version": context.SchemaVersion,
		"client_api_version": context.ClientAPIVersion} {
		if v, ok := r[field]; ok && !sameNumber(v, expected) {
			return "", invalid()
		}
	}
	if v, ok := r["manifest_sha256"]; ok && v != context.AssetHashes["release-manifest.json"] {
		return "", invalid()
	}
	if v, ok := r["checksums_sha256"]; ok && v != context.AssetHashes["SHA256SUMS.txt"] {
		return "", invalid()
	}
	for _, f := range []string{"observer", "date", "notes"} {
		if !text(r[f], 500) {
			return "", invalid()
		}
	}
	if r["obstacle"] != nil && !text(r["obstacle"], 2000) {
		return "", invalid()
	}
	refValue, hasReference := r["reference"]
	ref, referenceIsString := refValue.(string)
	if !hasReference || !referenceIsString {
		return "", invalid()
	}
	status, _ := r["status"].(string)
	if !(kind == LinuxFirstRun && ref == "" && (status == "not_run" || status == "incomplete")) && !referenceRE.MatchString(ref) {
		return "", invalid()
	}
	if kind == VoluntaryUsage {
		allowed := strings.Fields("kind reference candidate_sha candidate_version evidence_type return_use retrieval_one retrieval_two collection_restart obstacle status observer date notes")
		for k := range r {
			if !contains(allowed, k) {
				return "", invalid()
			}
		}
		if r["evidence_type"] != "manual" {
			return "", invalid()
		}
		st := "passed"
		if x, ok := r["status"]; ok {
			st, _ = x.(string)
		}
		if !oneOf(st, "passed failed not_run incomplete") {
			return "", invalid()
		}
		ret, ok := r["return_use"].(bool)
		if !ok {
			return "", invalid()
		}
		one, _ := r["retrieval_one"].(string)
		two, _ := r["retrieval_two"].(string)
		restart, _ := r["collection_restart"].(string)
		if !oneOf(one, "found not_found not_tried") || !oneOf(two, "found not_found not_tried") || !oneOf(restart, "restored not_restored not_tried") {
			return "", invalid()
		}
		if st == "failed" {
			return "failed", nil
		}
		if st == "passed" && ret && one != "not_tried" && two != "not_tried" && restart != "not_tried" {
			return "complete", nil
		}
		return "incomplete", nil
	}
	target := targets[kind]
	if r["target"] != target || r["evidence_type"] != "manual" || r["archive_sha256"] != context.NativeArchives[target] {
		return "", invalid()
	}
	if !oneOf(status, "passed failed not_run incomplete") {
		return "", invalid()
	}
	cases, ok := r["cases"].([]any)
	if !ok || len(cases) != len(caseNames) {
		return "", invalid()
	}
	complete := status == "passed"
	failed := status == "failed"
	for i, x := range cases {
		c, ok := x.(map[string]any)
		if !ok {
			return "", invalid()
		}
		for k := range c {
			if !contains(strings.Fields("case id status elapsed_seconds obstacle observation"), k) {
				return "", invalid()
			}
		}
		if _, a := c["case"]; a {
			if _, b := c["id"]; b {
				return "", invalid()
			}
		}
		name, ok := c["case"].(string)
		if !ok {
			name, _ = c["id"].(string)
		}
		if name != caseNames[i] {
			return "", invalid()
		}
		st, _ := c["status"].(string)
		if !oneOf(st, "passed failed not_run") {
			return "", invalid()
		}
		if st == "failed" {
			failed = true
		}
		v, present := c["elapsed_seconds"]
		measured := false
		if present && v != nil {
			f, e := number(v)
			if e != nil || f < 0 {
				return "", invalid()
			}
			measured = true
		}
		observation, present := c["obstacle"]
		if !present {
			observation = c["observation"]
		}
		if !text(observation, 2000) {
			return "", invalid()
		}
		complete = complete && st == "passed" && measured
	}
	if failed {
		return "failed", nil
	}
	if complete {
		return "complete", nil
	}
	return "incomplete", nil
}
func oneOf(s, choices string) bool {
	for _, v := range strings.Fields(choices) {
		if s == v {
			return true
		}
	}
	return false
}
func number(v any) (float64, error) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if (err != nil && f != 0) || math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, invalid()
		}
		return f, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return 0, invalid()
		}
		return n, nil
	default:
		return 0, invalid()
	}
}
func integer(v any) (int64, bool) {
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
func sameNumber(a, b any) bool {
	x, ok := integer(a)
	if !ok {
		return false
	}
	y, ok := integer(b)
	return ok && x == y
}
