package evaluation

import (
	"context"
	"encoding/json"
	"errors"
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
	var receivedQueries []string

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
		receivedQueries = append(receivedQueries, req.Query)

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
				{
					Path:     "b.md",
					SourceID: sourceID,
				},
			}
		default:
			t.Fatalf("unexpected request.Query=%q", req.Query)
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		t.Fatalf(
			"Run() error=%v, want nil",
			err,
		)
	}
	if report.Total != 2 {
		t.Errorf("report.Total=%d, want 2", report.Total)
	}
	if report.Passed != 1 {
		t.Errorf("report.Passed=%d, want 1", report.Passed)
	}
	if report.Failed != 1 {
		t.Errorf("report.Failed=%d, want 1", report.Failed)
	}
	if report.ExactMatchRate != 0.5 {
		t.Errorf("report.ExactMatchRate=%f, want 0.5", report.ExactMatchRate)
	}
	if !reflect.DeepEqual(receivedQueries, []string{"first", "second"}) {
		t.Errorf(
			"receivedQueries=%#v, want %#v",
			receivedQueries, []string{"first", "second"},
		)
	}

	if len(report.Results) != 2 {
		t.Fatalf(
			"len(report.Results)=%d, want 2",
			len(report.Results),
		)
	}

	firstResult := report.Results[0]
	firstMissingPaths := []string{"a.md"}
	firstExpectedPaths := []string{"a.md"}
	if firstResult.Query != "first" {
		t.Errorf(
			"firstResult.Query=%q, want first",
			firstResult.Query,
		)
	}
	if firstResult.Passed {
		t.Errorf(
			"firstResult.Passed=%t, want false",
			firstResult.Passed,
		)
	}
	if !reflect.DeepEqual(firstResult.ExpectedPaths, firstExpectedPaths) {
		t.Errorf(
			"firstResult.ExpectedPaths=%#v, want %#v",
			firstResult.ExpectedPaths, firstExpectedPaths,
		)
	}
	if len(firstResult.ActualPaths) != 0 || firstResult.ActualPaths == nil {
		t.Errorf(
			"firstResult.ActualPaths=%#v, want empty non-nil slice",
			firstResult.ActualPaths,
		)
	}
	if !reflect.DeepEqual(firstResult.MissingPaths, firstMissingPaths) {
		t.Errorf(
			"firstResult.MissingPaths=%#v, want %#v",
			firstResult.MissingPaths, firstMissingPaths,
		)
	}
	if len(firstResult.UnexpectedPaths) != 0 || firstResult.UnexpectedPaths == nil {
		t.Errorf(
			"firstResult.UnexpectedPaths=%#v, want empty non-nil slice",
			firstResult.UnexpectedPaths,
		)
	}

	secondResult := report.Results[1]
	secondExpectedPaths := []string{"b.md"}
	secondActualPaths := []string{"b.md"}
	if secondResult.Query != "second" {
		t.Errorf(
			"secondResult.Query=%q, want second",
			secondResult.Query,
		)
	}
	if !secondResult.Passed {
		t.Errorf(
			"secondResult.Passed=%t, want true",
			secondResult.Passed,
		)
	}
	if !reflect.DeepEqual(secondResult.ExpectedPaths, secondExpectedPaths) {
		t.Errorf(
			"secondResult.ExpectedPaths=%#v, want %#v",
			secondResult.ExpectedPaths, secondExpectedPaths,
		)
	}
	if !reflect.DeepEqual(secondResult.ActualPaths, secondActualPaths) {
		t.Errorf(
			"secondResult.ActualPaths=%#v, want %#v",
			secondResult.ActualPaths, secondActualPaths,
		)
	}
	if len(secondResult.MissingPaths) != 0 || secondResult.MissingPaths == nil {
		t.Errorf(
			"secondResult.MissingPaths=%#v, want empty non-nil slice",
			secondResult.MissingPaths,
		)
	}
	if len(secondResult.UnexpectedPaths) != 0 || secondResult.UnexpectedPaths == nil {
		t.Errorf(
			"secondResult.UnexpectedPaths=%#v, want empty non-nil slice",
			secondResult.UnexpectedPaths,
		)
	}
}

func TestRunStopsOnSearchError(t *testing.T) {
	var receivedQueries []string
	var searchErr = errors.New("search error")

	ctx := context.Background()
	sourceID := "test_source"
	cases := []Case{
		{
			Query:         "first",
			ExpectedPaths: []string{},
		},
		{
			Query:         "second",
			ExpectedPaths: []string{},
		},
		{
			Query:         "third",
			ExpectedPaths: []string{},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		receivedQueries = append(receivedQueries, req.Query)

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
			return response, nil
		case "second":
			return search.Response{}, searchErr
		case "third":
			t.Fatalf("third query must not run")
		default:
			t.Fatalf("unexpected request.Query=%q", req.Query)
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if !errors.Is(err, searchErr) {
		t.Fatalf(
			"error=%v, want %v",
			err, searchErr,
		)
	}
	if !reflect.DeepEqual(receivedQueries, []string{"first", "second"}) {
		t.Errorf(
			"receivedQueries=%#v, want %#v",
			receivedQueries, []string{"first", "second"},
		)
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Errorf("report=%#v, want zero Report", report)
	}
}

func TestRunIncompleteResults(t *testing.T) {
	var receivedQueries []string

	ctx := context.Background()
	sourceID := "test_source"
	cases := []Case{
		{
			Query:         "first",
			ExpectedPaths: []string{"a.md"},
		},
		{
			Query:         "second",
			ExpectedPaths: []string{},
		},
	}

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		receivedQueries = append(receivedQueries, req.Query)
		if req.SourceID != sourceID {
			t.Errorf(
				"req.SourceID=%q, want %q",
				req.SourceID, sourceID,
			)
		}
		if req.Limit != 100 {
			t.Errorf(
				"req.Limit=%d, want %d",
				req.Limit, 100,
			)
		}

		response := search.Response{}

		switch req.Query {
		case "first":
			response.Query = req.Query
			response.Total = 2
			response.Results = append(response.Results, search.Result{
				Path:     "a.md",
				SourceID: sourceID,
			})
		case "second":
			t.Fatalf("second query must not run")
		default:
			t.Fatalf("unexpected req.Query=%q", req.Query)
		}
		return response, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if !errors.Is(err, ErrIncompleteResults) {
		t.Errorf(
			"Run() error=%v, want %v",
			err, ErrIncompleteResults,
		)
	}
	if !reflect.DeepEqual(receivedQueries, []string{"first"}) {
		t.Errorf(
			"receivedQueries=%#v, want %#v",
			receivedQueries, []string{"first"},
		)
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Errorf("report=%#v, want zero Report", report)
	}
}

func TestRunCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []Case{
		{
			Query:         "first",
			ExpectedPaths: []string{},
		},
	}
	sourceID := "test_source"

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		t.Fatalf("search must not run")
		return search.Response{}, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error=%v, want %v", err, context.Canceled)
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Errorf("report=%#v, want %#v", report, Report{})
	}
}

func TestRunCanceledBetweenCases(t *testing.T) {
	receivedQueries := []string{}
	searchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cases := []Case{
		{
			Query:         "first",
			ExpectedPaths: []string{},
		},
		{
			Query:         "second",
			ExpectedPaths: []string{},
		},
	}
	sourceID := "test_source"

	engine := fakeEngine(func(ctx context.Context, req search.Request) (search.Response, error) {
		receivedQueries = append(receivedQueries, req.Query)

		if req.SourceID != sourceID {
			t.Errorf(
				"req.SourceID=%q, want %q",
				req.SourceID, sourceID,
			)
		}
		if req.Limit != 100 {
			t.Errorf(
				"req.Limit=%d, want %d",
				req.Limit, 100,
			)
		}

		response := search.Response{}

		switch req.Query {
		case "first":
			cancel()
			if !errors.Is(searchCtx.Err(), context.Canceled) {
				t.Errorf(
					"ctx error=%v, want %v",
					searchCtx.Err(), context.Canceled,
				)
			}
			return response, nil
		case "second":
			t.Fatalf("second query must not run")
		default:
			t.Fatalf("unexpected requet.Query=%q", req.Query)
		}
		return search.Response{}, nil
	})
	report, err := Run(searchCtx, engine, cases, sourceID)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error=%v, want %v", err, context.Canceled)
	}
	if !reflect.DeepEqual(receivedQueries, []string{"first"}) {
		t.Errorf("receivedQueries=%#v, want %#v", receivedQueries, []string{"first"})
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Errorf("report=%#v, want %#v", report, Report{})
	}
}

func TestRunInvalidResponse(t *testing.T) {
	type testCase struct {
		name     string
		response search.Response
	}

	ctx := context.Background()
	sourceID := "test_source"

	cases := []Case{
		{
			Query:         "first",
			ExpectedPaths: []string{},
		},
	}

	scenes := []testCase{
		{
			name: "negative_total",
			response: search.Response{
				Total:   -1,
				Results: []search.Result{},
			},
		},
		{
			name: "total_less_than_results",
			response: search.Response{
				Total: 0,
				Results: []search.Result{
					{
						Path:     "a.md",
						SourceID: sourceID,
					},
				},
			},
		},
		{
			name: "duplicate_paths",
			response: search.Response{
				Total: 2,
				Results: []search.Result{
					{
						Path:     "a.md",
						SourceID: sourceID,
					},
					{
						Path:     "a.md",
						SourceID: sourceID,
					},
				},
			},
		},
		{
			name: "wrong_source",
			response: search.Response{
				Total: 1,
				Results: []search.Result{
					{
						Path:     "c.md",
						SourceID: "other_source",
					},
				},
			},
		},
	}

	for _, scene := range scenes {
		t.Run(scene.name, func(t *testing.T) {
			calls := 0

			engine := fakeEngine(func(ctx context.Context, r search.Request) (search.Response, error) {
				calls++
				return scene.response, nil
			})

			report, err := Run(ctx, engine, cases, sourceID)
			if err == nil {
				t.Errorf("Run() error=nil, want not nil")
			}
			if !reflect.DeepEqual(report, Report{}) {
				t.Errorf("report=%#v, want %#v", report, Report{})
			}
			if calls != 1 {
				t.Errorf("Search calls=%d, want 1", calls)
			}
		})
	}
}

func TestRunInvalidInput(t *testing.T) {
	type testCase struct {
		name     string
		sourceID string
		cases    []Case
	}

	tooManyCases := make([]Case, 0, 101)
	for i := range tooManyCases {
		tooManyCases[i] = Case{
			Query:         "hello",
			ExpectedPaths: []string{},
		}
	}

	tooManyExpectedPaths := []string{}

	for i := 0; i < 101; i++ {
		tooManyExpectedPaths = append(tooManyExpectedPaths, fmt.Sprintf("file%d.md", i+1))
	}

	ctx := context.Background()

	scenes := []testCase{
		{
			name:     "empty_source",
			sourceID: "",
			cases: []Case{
				{
					Query:         "first",
					ExpectedPaths: []string{},
				},
			},
		},
		{
			name:     "empty_cases",
			sourceID: "test_source",
			cases:    []Case{},
		},
		{
			name:     "invalid_query",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "!!!",
					ExpectedPaths: []string{},
				},
			},
		},
		{
			name:     "nil_expected_paths",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "fourth",
					ExpectedPaths: nil,
				},
			},
		},
		{
			name:     "duplicate_expected_paths",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "fifth",
					ExpectedPaths: []string{"a.md", "a.md"},
				},
			},
		},
		{
			name:     "invalid_second_case",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "sixth",
					ExpectedPaths: []string{},
				},
				{
					Query:         "seventh",
					ExpectedPaths: []string{"../a.md"},
				},
			},
		},
		{
			name:     "too_many_cases",
			sourceID: "test_source",
			cases:    tooManyCases,
		},
		{
			name:     "too_many_expected_paths",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "eighth",
					ExpectedPaths: tooManyExpectedPaths,
				},
			},
		},
		{
			name:     "empty_path",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "nineth",
					ExpectedPaths: []string{""},
				},
			},
		},
		{
			name:     "slash_path",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "tenth",
					ExpectedPaths: []string{"/a.md"},
				},
			},
		},
		{
			name:     "dot_path",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "eleventh",
					ExpectedPaths: []string{"."},
				},
			},
		},
		{
			name:     "two_dot_path",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "twelfth",
					ExpectedPaths: []string{".."},
				},
			},
		},
		{
			name:     "path_with_dots",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "thirteenth",
					ExpectedPaths: []string{"folder/../a.md"},
				},
			},
		},
		{
			name:     "path_with_backslash",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "fourteenth",
					ExpectedPaths: []string{"folder\\a.md"},
				},
			},
		},
		{
			name:     "windows_prefix_path",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "fifteenth",
					ExpectedPaths: []string{"C:/a.md"},
				},
			},
		},
		{
			name:     "windows_path_without_slash",
			sourceID: "test_source",
			cases: []Case{
				{
					Query:         "sixteenth",
					ExpectedPaths: []string{"C:a.md"},
				},
			},
		},
	}

	for _, scene := range scenes {
		t.Run(scene.name, func(t *testing.T) {
			calls := 0

			engine := fakeEngine(func(ctx context.Context, r search.Request) (search.Response, error) {
				calls++
				return search.Response{}, nil
			})

			report, err := Run(ctx, engine, scene.cases, scene.sourceID)
			if err == nil {
				t.Errorf("Run() error=nil, want not nil")
			}
			if !reflect.DeepEqual(report, Report{}) {
				t.Errorf("report=%#v, want %#v", report, Report{})
			}
			if calls != 0 {
				t.Errorf("Search calls=%d, want 0", calls)
			}
		})
	}
}

func TestReportJSON(t *testing.T) {
	ctx := context.Background()

	sourceID := "test_source"

	cases := []Case{
		{
			Query:         "hello",
			ExpectedPaths: []string{},
		},
	}

	engine := fakeEngine(func(ctx context.Context, r search.Request) (search.Response, error) {
		return search.Response{
			Total: 0,
		}, nil
	})

	report, err := Run(ctx, engine, cases, sourceID)
	if err != nil {
		t.Fatalf("Run() error=%v, want nil error", err)
	}

	data, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatalf("json.Marshal() error=%v, want nil error", marshalErr)
	}

	fields := make(map[string]json.RawMessage)
	unmarshalErr := json.Unmarshal(data, &fields)
	if unmarshalErr != nil {
		t.Fatalf("json.Unmarshal() error=%v, want nil error", unmarshalErr)
	}

	names := []string{"source_id", "total", "passed", "failed", "exact_match_rate", "results"}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			t.Errorf("no key %q", name)
		}
	}

	var results []map[string]json.RawMessage
	resultErr := json.Unmarshal(fields["results"], &results)
	if resultErr != nil {
		t.Fatalf("json.Unmarshal() error=%v, want nil error", resultErr)
	}
	if len(results) != 1 {
		t.Fatalf("len(results)=%d, want 1", len(results))
	}
	resultNames := []string{
		"query", "expected_paths", "actual_paths",
		"missing_paths", "unexpected_paths", "passed",
	}
	for _, name := range resultNames {
		if _, ok := results[0][name]; !ok {
			t.Errorf("no key %q", name)
		}
	}

	emptyArrays := []string{
		"expected_paths", "actual_paths", "missing_paths", "unexpected_paths",
	}
	for _, name := range emptyArrays {
		if string(results[0][name]) != "[]" {
			t.Errorf("array.%s=%s, want []", name, results[0][name])
		}
	}

	if string(fields["total"]) != "1" {
		t.Errorf("fields[total]=%q, want 1", string(fields["total"]))
	}
	if string(fields["passed"]) != "1" {
		t.Errorf("fields[passed]=%q, want 1", string(fields["passed"]))
	}
	if string(fields["failed"]) != "0" {
		t.Errorf("fields[failed]=%q, want 0", string(fields["failed"]))
	}
	if string(fields["exact_match_rate"]) != "1" {
		t.Errorf("fields[exact_match_rate]=%q, want 1", string(fields["exact_match_rate"]))
	}
	if string(results[0]["passed"]) != "true" {
		t.Errorf("results[0][passed]=%q, want true", string(results[0]["passed"]))
	}
}
