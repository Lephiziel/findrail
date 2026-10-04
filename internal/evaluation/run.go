package evaluation

import (
	"context"
	"fmt"
	"sort"

	"github.com/Lephiziel/findrail/internal/search"
)

func validateCases(cases []Case) error {
	if len(cases) < 1 || len(cases) > 100 {
		return fmt.Errorf("len of cases not in range, len=%d", len(cases))
	}

	for _, c := range cases {
		expectedPaths := make(map[string]struct{})

		_, err := search.Expression(c.Query)

		if err != nil {
			return err
		}
		if c.ExpectedPaths == nil {
			return fmt.Errorf("no expected paths")
		}
		if len(c.ExpectedPaths) > 100 {
			return fmt.Errorf("len of expected paths not in range, len=%d", len(c.ExpectedPaths))
		}

		for _, path := range c.ExpectedPaths {
			if _, ok := expectedPaths[path]; ok {
				return fmt.Errorf("duplicate of key")
			}
			expectedPaths[path] = struct{}{}
		}
	}
	return nil
}

func Run(ctx context.Context, engine search.Engine, cases []Case, sourceID string) (Report, error) {
	if sourceID == "" {
		return Report{}, fmt.Errorf("sourceID is empty")
	}
	if err := validateCases(cases); err != nil {
		return Report{}, err
	}

	results := make([]CaseResult, 0, len(cases))

	for _, c := range cases {
		actualSet := make(map[string]struct{})
		expectedSet := make(map[string]struct{})
		expected := make([]string, len(c.ExpectedPaths))
		copy(expected, c.ExpectedPaths)
		sort.Strings(expected)

		actualPaths := make([]string, 0, 1)
		missingPaths := make([]string, 0, 1)
		unexpectedPaths := make([]string, 0, 1)

		req := search.Request{
			Query:    c.Query,
			SourceID: sourceID,
			Limit:    100,
		}

		if ctx.Err() != nil {
			return Report{}, ctx.Err()
		}

		res, err := engine.Search(ctx, req)
		if err != nil {
			return Report{}, fmt.Errorf("the engine returned an error: %w", err)
		}
		if res.Total < len(res.Results) || res.Total < 0 {
			return Report{}, fmt.Errorf("len of the array slice more then total num or total is a negative number")
		}

		for _, r := range res.Results {
			actualPaths = append(actualPaths, r.Path)

			if _, ok := actualSet[r.Path]; ok {
				return Report{}, fmt.Errorf("duplicate of the key")
			}
			if r.ID != sourceID {
				return Report{}, fmt.Errorf("IDs are not matching")
			}

			actualSet[r.Path] = struct{}{}
		}
		for _, path := range c.ExpectedPaths {
			expectedSet[path] = struct{}{}
		}

		for k := range expectedSet {
			if _, ok := actualSet[k]; !ok {
				missingPaths = append(missingPaths, k)
			}
		}

		for k := range actualSet {
			if _, ok := expectedSet[k]; !ok {
				unexpectedPaths = append(unexpectedPaths, k)
			}
		}

		caseResult := CaseResult{
			Query:           c.Query,
			ExpectedPaths:   c.ExpectedPaths,
			ActualPaths:     actualPaths,
			MissingPaths:    missingPaths,
			UnexpectedPaths: unexpectedPaths,
			Passed:          true,
		}

		if len(missingPaths) != 0 || len(unexpectedPaths) != 0 {
			caseResult.Passed = false
		}

		results = append(results, caseResult)
	}

	report := Report{
		SourceID: sourceID,
		Total:    len(results),
		Results:  results,
	}

	for _, r := range results {
		if r.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
	}

	report.ExactMatchRate = float64(report.Passed) / float64(report.Total)

	return report, nil
}
