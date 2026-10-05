package evaluation

import (
	"context"
	"fmt"

	"testing"

	"github.com/Lephiziel/findrail/internal/search"
)

type fakeEngine func(context.Context, search.Request) (search.Response, error)

func (f fakeEngine) Search(ctx context.Context, req search.Request) (search.Response, error) {
	return f(ctx, req)
}

func TestRunExactMatch(t *testing.T) {
	ctx := context.Background()
	sourceID := "test_source"
	cases := []Case{
		Case{
			Query:         "hello",
			ExpectedPaths: []string{"a.md", "b.md"},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		if req.Query == "" {
			return search.Response{}, fmt.Errorf("query is empty")
		}
		if req.SourceID == "" {
			return search.Response{}, fmt.Errorf("source_id is empty")
		}
		if req.Limit < 1 || req.Limit > 100 {
			return search.Response{}, fmt.Errorf("limit not in range")
		}

		response := search.Response{
			Query: req.Query,
			Total: 2,
			Results: []search.Result{
				{
					ID:         "1",
					Title:      "name1",
					URI:        "uri1",
					Path:       "a.md",
					SourceID:   "test_source",
					SourceName: "TestSource",
					SourceKind: "SourceKind",
					Snippet:    "Snippet1",
					Score:      float64(3.14),
					MediaType:  ".md",
					PageCount:  1,
				},
				{
					ID:         "2",
					Title:      "name2",
					URI:        "uri2",
					Path:       "b.md",
					SourceID:   "test_source",
					SourceName: "TestSource",
					SourceKind: "SourceKind",
					Snippet:    "Snippet2",
					Score:      float64(3.14),
					MediaType:  ".md",
					PageCount:  1,
				},
			},
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		t.Fatalf("expected no errors, but we got %v", err)
	}
	if report.SourceID != sourceID {
		t.Errorf("expected source_id=%q, but we got source_id=%q", sourceID, report.SourceID)
	}
	if report.Total != 1 {
		t.Errorf("expected total=1, but we got total=%d", report.Total)
	}
	if report.Failed != 0 {
		t.Errorf("expected failed=0, but we got failed=%d", report.Failed)
	}
	if report.ExactMatchRate != 1.0 {
		t.Errorf("expected exact_match_rate=1.0, but we got expected exact_match_rate=%f", report.ExactMatchRate)
	}
	if len(report.Results) != 1 {
		t.Fatalf("expected len of results=1, but we got len(results)=%d", len(report.Results))
	}
	if !report.Results[0].Passed {
		t.Errorf("asd")
	}
	if report.Results[0].ExpectedPaths[0] != "a.md" ||
		report.Results[0].ExpectedPaths[1] != "b.md" {
		t.Errorf(
			"expected expected_paths a.md and b.md, but we got %q and %q",
			report.Results[0].ExpectedPaths[0], report.Results[0].ExpectedPaths[1],
		)
	}
	if report.Results[0].ActualPaths[0] != "a.md" ||
		report.Results[0].ActualPaths[1] != "b.md" {
		t.Errorf(
			"expected actual_paths a.md and b.md, but we got %q and %q",
			report.Results[0].ActualPaths[0], report.Results[0].ActualPaths[1],
		)
	}
	if len(report.Results[0].MissingPaths) != 0 ||
		len(report.Results[0].UnexpectedPaths) != 0 {
		t.Errorf(
			"expected missing paths and unexpected paths are empty, but we got not empty",
		)
	}
}
