package evaluation

import (
	"context"
	"fmt"
	"path/filepath"
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

		for _, path := range c.ExpectedPaths {
			if _, ok := seenPaths[path]; ok {
				return fmt.Errorf(
					"case %d: expected_paths: duplicate path %q",
					i+1, path,
				)
			}
			seenPaths[path] = struct{}{}
		}
	}
	return nil
}

func validateExpectedPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("absolute file path")
	}

	parts := strings.Split(p, string(filepath.Separator))
	for _, part := range parts {
		if part == "." || part == ".." {
			return fmt.Errorf(". and .. are not allowed")
		}
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("back slashes are not allowed")
	}
	if filepath.VolumeName(p) != "" {
		return fmt.Errorf("windows prefixes are not allowed")
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

		actualPaths := make([]string, 0, 0)
		missingPaths := make([]string, 0, 0)
		unexpectedPaths := make([]string, 0, 0)

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
			if err := validateExpectedPath(path); err != nil {
				return Report{}, fmt.Errorf(
					"case %d: expected_paths: invalid path %q: %w",
					i+1, path, err,
				)
			}
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
