package evaluation

type Case struct {
	Query         string   `json:"query"`
	ExpectedPaths []string `json:"expected_paths"`
	Language      string   `json:"language"`
	Reason        string   `json:"reason"`
}

type CaseResult struct {
	Query           string   `json:"query"`
	ExpectedPaths   []string `json:"expected_paths"`
	ActualPaths     []string `json:"actual_paths"`
	MissingPaths    []string `json:"missing_paths"`
	UnexpectedPaths []string `json:"unexpected_paths"`
	Passed          bool     `json:"passed"`
}

type Report struct {
	SourceID       string       `json:"source_id"`
	Total          int          `json:"total"`
	Passed         int          `json:"passed"`
	Failed         int          `json:"failed"`
	ExactMatchRate float64      `json:"exact_match_rate"`
	Results        []CaseResult `json:"results"`
}
