package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPassageBudgetRequiresProgress(t *testing.T) {
	s, err := New(testBackend(), Config{AllowedSources: []string{"a"}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	r := evidenceOutput{ID: "doc-a", SourceID: "a", Text: strings.Repeat("😀", 1000), TextChars: 1000, TextStartChar: intPtr(17), TextTotalChars: intPtr(2000)}
	updateContinuation(&r)
	empty := r
	empty.Text, empty.TextChars, empty.Truncated = "", 0, true
	empty.TruncationReasons = []string{"mcp_response_budget"}
	updateContinuation(&empty)
	s.resultOverhead = 0
	wire, err := s.renderResult(empty, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	s.resultOverhead = MaxResultBytes - len(data)
	got, _, err := s.fitEvidence(r)
	if err != nil || !got.IsError {
		t.Fatalf("zero-progress window accepted: %v %+v", err, got)
	}
}

func TestLegacyEvidenceOmitsUnknownPassageBounds(t *testing.T) {
	s, err := New(testBackend(), Config{AllowedSources: []string{"a"}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.evidence(context.Background(), nil, evidenceInput{SourceID: "a", DocumentID: "doc-a"})
	if err != nil || r.IsError {
		t.Fatalf("legacy evidence: %v %+v", err, r)
	}
	var fields map[string]any
	decodeStructured(t, r.StructuredContent, &fields)
	for _, key := range []string{"text_start_char", "text_total_chars", "has_more", "next_start_char"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("legacy evidence claimed unknown %s: %+v", key, fields)
		}
	}
	r, _, err = s.evidence(context.Background(), nil, evidenceInput{SourceID: "a", DocumentID: "doc-a", OriginalIdentity: "stale"})
	if err != nil || !r.IsError {
		t.Fatalf("explicit legacy identity guard was ignored: %v %+v", err, r)
	}
	query := "hello"
	r, _, err = s.evidence(context.Background(), nil, evidenceInput{SourceID: "a", DocumentID: "doc-a", Query: &query, OriginalIdentity: "snapshot"})
	if err != nil || !r.IsError {
		t.Fatalf("unsupported passage backend accepted: %v %+v", err, r)
	}
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeStructured(t, r.StructuredContent, &failure)
	if failure.Error.Code != "unsupported" {
		t.Fatalf("unsupported backend error: %+v", failure)
	}
}
