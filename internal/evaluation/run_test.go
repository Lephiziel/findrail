package evaluation

import (
	"context"
	"fmt"
	"reflect"
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
		{
			Query:         "hello",
			ExpectedPaths: []string{"b.md", "a.md"},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		if req.Query != "hello" {
			t.Errorf(
				"request.Query=%q, want %q",
				req.Query, "hello",
			)
		}
		if req.SourceID != sourceID {
			t.Errorf(
				"request.SourceID=%q, want %q",
				req.SourceID, sourceID,
			)
		}
		if req.Limit != 100 {
			t.Errorf(
				"request.Limit=%d, want %d",
				req.Limit, 100,
			)
		}

		response := search.Response{
			Query: req.Query,
			Total: 2,
			Results: []search.Result{
				{
					Path:     "a.md",
					SourceID: sourceID,
				},
				{
					Path:     "b.md",
					SourceID: sourceID,
				},
			},
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		t.Fatalf("Run() error=%v, want nil", err)
	}
	if len(cases[0].ExpectedPaths) != 2 {
		t.Fatalf(
			"len(expected_paths)=%d, want 2",
			len(cases[0].ExpectedPaths),
		)
	}

	if cases[0].ExpectedPaths[0] != "b.md" ||
		cases[0].ExpectedPaths[1] != "a.md" {
		t.Errorf(
			"cases[0].ExpectedPaths=%v, want [b.md a.md]",
			cases[0].ExpectedPaths,
		)
	}
	if report.SourceID != sourceID {
		t.Errorf("source_id=%q, want %q", report.SourceID, sourceID)
	}
	if report.Total != 1 {
		t.Errorf("total=%d, want 1", report.Total)
	}
	if report.Failed != 0 {
		t.Errorf("failed=%d, want 0", report.Failed)
	}
	if report.ExactMatchRate != 1.0 {
		t.Errorf("exact_match_rate=%f, want 1.0", report.ExactMatchRate)
	}
	if len(report.Results) != 1 {
		t.Fatalf("len(report.Results)=%d, want 1", len(report.Results))
	}

	result := report.Results[0]

	if !result.Passed {
		t.Errorf(
			"result.passed=%t, want true",
			result.Passed,
		)
	}
	if report.Passed != 1 {
		t.Errorf(
			"passed=%d, want 1",
			report.Passed,
		)
	}

	wantPaths := []string{"a.md", "b.md"}

	if !reflect.DeepEqual(result.ExpectedPaths, wantPaths) {
		t.Errorf(
			"result.ExpectedPaths=%v, want %v",
			result.ExpectedPaths, wantPaths,
		)
	}

	if !reflect.DeepEqual(result.ActualPaths, wantPaths) {
		t.Errorf(
			"result.ActualPaths=%v, want %v",
			result.ActualPaths, wantPaths,
		)
	}
	if len(result.MissingPaths) != 0 || result.MissingPaths == nil {
		t.Errorf(
			"len(result.MissingPaths)=%#v, want non-nil slice",
			len(result.MissingPaths),
		)
	}
	if len(result.UnexpectedPaths) != 0 || result.UnexpectedPaths == nil {
		t.Errorf(
			"len(result.UnexpectedPaths)=%#v, want non-nil slice",
			len(result.UnexpectedPaths),
		)
	}
}

func TestRunMismatch(t *testing.T) {
	ctx := context.Background()
	sourceID := "test_source"
	cases := []Case{
		{
			Query:         "hello",
			ExpectedPaths: []string{"a.md", "b.md"},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		if req.Query != "hello" {
			t.Errorf(
				"request.Query=%q, want %q",
				req.Query, "hello",
			)
		}
		if req.SourceID != sourceID {
			t.Errorf(
				"request.SourceID=%q, want %q",
				req.SourceID, sourceID,
			)
		}
		if req.Limit != 100 {
			t.Errorf(
				"request.Limit=%d, want %d",
				req.Limit, 100,
			)
		}

		response := search.Response{
			Query: req.Query,
			Total: 2,
			Results: []search.Result{
				{
					Path:     "c.md",
					SourceID: sourceID,
				},
				{
					Path:     "b.md",
					SourceID: sourceID,
				},
			},
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		t.Fatalf("Run() error=%v, want nil", err)
	}
	if report.Total != 1 {
		t.Errorf("report.Total=%d, want 1", report.Total)
	}
	if report.Passed != 0 {
		t.Errorf("report.Passed=%d, want 0", report.Passed)
	}
	if report.Failed != 1 {
		t.Errorf("report.Failed=%d, want 1", report.Failed)
	}
	if report.ExactMatchRate != 0 {
		t.Errorf("report.ExactMatchRate=%f, want 0", report.ExactMatchRate)
	}
	if len(report.Results) != 1 {
		t.Fatalf("len(report.Results)=%d, want 1", len(report.Results))
	}

	wantExpectedPaths := []string{"a.md", "b.md"}
	wantActualPaths := []string{"b.md", "c.md"}
	wantMissingPaths := []string{"a.md"}
	wantUnexpectedPaths := []string{"c.md"}
	result := report.Results[0]
	if result.Passed {
		t.Errorf("result.Passed=%t, want false", result.Passed)
	}
	if !reflect.DeepEqual(result.ExpectedPaths, wantExpectedPaths) {
		t.Errorf(
			"result.ExpectedPaths=%#v, want %#v",
			result.ExpectedPaths, wantExpectedPaths,
		)
	}
	if !reflect.DeepEqual(result.ActualPaths, wantActualPaths) {
		t.Errorf(
			"result.ActualPaths=%#v, want %#v",
			result.ActualPaths, wantActualPaths,
		)
	}
	if !reflect.DeepEqual(result.MissingPaths, wantMissingPaths) {
		t.Errorf(
			"result.MissingPaths=%#v, want %#v",
			result.MissingPaths, wantMissingPaths,
		)
	}
	if !reflect.DeepEqual(result.UnexpectedPaths, wantUnexpectedPaths) {
		t.Errorf(
			"result.UnexpectedPaths=%#v, want %#v",
			result.UnexpectedPaths, wantUnexpectedPaths,
		)
	}
}

func TestRunEmptyResults(t *testing.T) {
	ctx := context.Background()
	sourceID := "test_source"
	cases := []Case{
		{
			Query:         "asd",
			ExpectedPaths: []string{},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		if req.Query != "asd" {
			t.Errorf(
				"request.Query=%q, want %q",
				req.Query, "asd",
			)
		}
		if req.SourceID != sourceID {
			t.Errorf(
				"request.SourceID=%q, want %q",
				req.SourceID, sourceID,
			)
		}
		if req.Limit != 100 {
			t.Errorf(
				"request.Limit=%d, want %d",
				req.Limit, 100,
			)
		}

		response := search.Response{
			Query: req.Query,
			Total: 0,
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		t.Fatalf("Run() error=%v, want nil", err)
	}
	if report.Passed != 1 {
		t.Errorf("report.Passed=%d, want 1", report.Passed)
	}
	if report.Total != 1 {
		t.Errorf("report.Total=%d, want 1", report.Total)
	}
	if report.ExactMatchRate != 1.0 {
		t.Errorf("exact_match_rate=%f, want 1.0", report.ExactMatchRate)
	}
	if report.Failed != 0 {
		t.Errorf("report.Failed=%d, want 0", report.Failed)
	}
	if len(report.Results) != 1 {
		t.Fatalf("len(report.Results)=%d, want 1", len(report.Results))
	}

	result := report.Results[0]
	if !result.Passed {
		t.Errorf("result.Passed=%t, want true", result.Passed)
	}
	if len(result.ExpectedPaths) != 0 || result.ExpectedPaths == nil {
		t.Errorf(
			"result.ExpectedPaths=%#v, want empty non-nil slice",
			result.ExpectedPaths,
		)
	}
	if len(result.ActualPaths) != 0 || result.ActualPaths == nil {
		t.Errorf(
			"result.ActualPaths=%#v, want empty non-nil slice",
			result.ActualPaths,
		)
	}
	if len(result.MissingPaths) != 0 || result.MissingPaths == nil {
		t.Errorf(
			"result.MissingPaths=%#v, want empty non-nil slice",
			result.MissingPaths,
		)
	}
	if len(result.UnexpectedPaths) != 0 || result.UnexpectedPaths == nil {
		t.Errorf(
			"result.UnexpectedPaths=%#v, want empty non-nil slice",
			result.UnexpectedPaths,
		)
	}
}

func TestRunContinuesAfterMismatch(t *testing.T) {
	var receivedQuries []string

	ctx := context.Background()
	sourceID := "test_source"
	cases := []Case{
		{
			Query:         "first",
			ExpectedPaths: []string{"a.md"},
		},
		{
			Query:         "second",
			ExpectedPaths: []string{"b.md"},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		receivedQuries = append(receivedQuries, req.Query)

		if req.Query != "first" && req.Query != "second" {
			t.Errorf(
				"request.Query=%q, want first or second",
				req.Query,
			)
		}
		if req.SourceID != sourceID {
			t.Errorf(
				"request.SourceID=%q, want %q",
				req.SourceID, sourceID,
			)
		}
		if req.Limit != 100 {
			t.Errorf(
				"request.Limit=%d, want %d",
				req.Limit, 100,
			)
		}

		response := search.Response{}

		switch req.Query {
		case "first":
			response.Query = req.Query
			response.Total = 0
		case "second":
			response.Query = req.Query
			response.Total = 1
			response.Results = []search.Result{
				{},
			}
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		fmt.Println(report)
	}
}
