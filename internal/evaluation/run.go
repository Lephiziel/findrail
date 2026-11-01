package evaluation

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Lephiziel/findrail/internal/search"
)

func validateCases(cases []Case) error {
	if len(cases) < 1 || len(cases) > 100 {
		return fmt.Errorf("expected 1 to 100 cases, got %d", len(cases))
	}

	for i, c := range cases {
		seenPaths := make(map[string]struct{})

		if _, err := search.Expression(c.Query); err != nil {
			return fmt.Errorf("case %d: query: %w", i+1, err)
		}
		if c.ExpectedPaths == nil {
			return fmt.Errorf(
				"case %d: expected_paths must be a non-null array",
				i+1,
			)
		}
		if len(c.ExpectedPaths) > 100 {
			return fmt.Errorf(
				"case %d: expected_paths: maximum 100 paths, got %d",
				i+1, len(c.ExpectedPaths),
			)
		}

		for _, p := range c.ExpectedPaths {
			if err := validateExpectedPath(p); err != nil {
				return fmt.Errorf(
					"case %d: expected_paths: invalid path %q: %w",
					i+1, p, err,
				)
			}
			if _, ok := seenPaths[p]; ok {
				return fmt.Errorf(
					"case %d: expected_paths: duplicate path %q",
					i+1, p,
				)
			}

			seenPaths[p] = struct{}{}
		}
	}
	return nil
}

func validateExpectedPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if path.IsAbs(p) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("backslashes are not allowed")
	}

	// Reject Windows drive prefixes regardless of the host OS
	if len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') ||
			(p[0] >= 'a' && p[0] <= 'z')) {
		return fmt.Errorf("Windows drive prefixes are not allowed")
	}

	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("dot path segments are not allowed")
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

	for i, c := range cases {
		actualSet := make(map[string]struct{})
		expectedSet := make(map[string]struct{})
		expected := make([]string, len(c.ExpectedPaths))
		copy(expected, c.ExpectedPaths)
		sort.Strings(expected)

		actualPaths := make([]string, 0)
		missingPaths := make([]string, 0)
		unexpectedPaths := make([]string, 0)

		req := search.Request{
			Query:    c.Query,
			SourceID: sourceID,
			Limit:    100,
		}

		if err := ctx.Err(); err != nil {
			return Report{}, fmt.Errorf("case %d: %w", i+1, err)
		}

		res, err := engine.Search(ctx, req)
		if err != nil {
			return Report{}, fmt.Errorf("case %d: search: %w", i+1, err)
		}
		if res.Total < len(res.Results) || res.Total < 0 {
			return Report{}, fmt.Errorf(
				"case %d: invalid search response: total=%d, results=%d",
				i+1, res.Total, len(res.Results),
			)
		}
		if res.Total > len(res.Results) {
			return Report{}, fmt.Errorf(
				"case %d: %w: received %d of %d results",
				i+1, ErrIncompleteResults, len(res.Results), res.Total,
			)
		}

		for _, r := range res.Results {
			if _, ok := actualSet[r.Path]; ok {
				return Report{}, fmt.Errorf("case %d: duplicate result path %q", i+1, r.Path)
			}
			if r.SourceID != sourceID {
				return Report{}, fmt.Errorf(
					"case %d: source mismatch expected %q, got %q",
					i+1, sourceID, r.SourceID,
				)
			}

			actualPaths = append(actualPaths, r.Path)
			actualSet[r.Path] = struct{}{}
		}
		for _, path := range c.ExpectedPaths {
			expectedSet[path] = struct{}{}
		}

		for p := range expectedSet {
			if _, ok := actualSet[p]; !ok {
				missingPaths = append(missingPaths, p)
			}
		}

		for p := range actualSet {
			if _, ok := expectedSet[p]; !ok {
				unexpectedPaths = append(unexpectedPaths, p)
			}
		}

		sort.Strings(actualPaths)
		sort.Strings(missingPaths)
		sort.Strings(unexpectedPaths)

		caseResult := CaseResult{
			Query:           c.Query,
			ExpectedPaths:   expected,
			ActualPaths:     actualPaths,
			MissingPaths:    missingPaths,
			UnexpectedPaths: unexpectedPaths,
			Passed:          len(missingPaths) == 0 && len(unexpectedPaths) == 0,
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
