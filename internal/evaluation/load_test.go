package evaluation

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLoadCasesValid(t *testing.T) {
	wantCases := []Case{
		{
			Query:         "hello",
			ExpectedPaths: []string{},
			Language:      "English",
			Reason:        "SomeReason",
		},
	}

	data := `[{"query":"hello","expected_paths":[],"language":"English","reason":"SomeReason"}]`

	cases, err := LoadCases(strings.NewReader(data))
	if err != nil {
		t.Fatalf("LoadCases() error=%v, want nil", err)
	}
	if !reflect.DeepEqual(cases, wantCases) {
		t.Errorf("cases=%#v, want %#v", cases, wantCases)
	}
}

func TestLoadCasesInvalidJSON(t *testing.T) {
	type testCase struct {
		name  string
		input string
	}

	loadedCases := []testCase{
		{
			name:  "malformed_json",
			input: `[{"query":"hello","expected_paths":[],"language":"English","reason":"SomeReason"}`,
		},
		{
			name:  "second_value",
			input: `[{"query":"hello","expected_paths":[],"language":"English","reason":"SomeReason"}] 123`,
		},
		{
			name:  "trailing_garbage",
			input: `[{"query":"hello","expected_paths":[],"language":"English","reason":"SomeReason"}] ]s`,
		},
		{
			name:  "unknown_field",
			input: `[{"query":"hello","expected_paths":[],"language":"English","reason":"SomeReason","typo":true}]`,
		},
		{
			name:  "missing_expected_paths",
			input: `[{"query":"hello","language":"English","reason":"SomeReason"}]`,
		},
		{
			name:  "null_expected_paths",
			input: `[{"query":"hello","expected_paths":null,"language":"English","reason":"SomeReason"}]`,
		},
	}
	for _, scene := range loadedCases {
		t.Run(scene.name, func(t *testing.T) {
			cases, err := LoadCases(strings.NewReader(scene.input))
			if err == nil {
				t.Errorf("LoadCases() error is nil, want error")
			}
			if cases != nil {
				t.Errorf("cases=%#v, want nil slice", cases)
			}
		})
	}
}

func TestLoadCasesInputSize(t *testing.T) {
	type testCase struct {
		name      string
		input     string
		repeat    int
		wantError bool
	}

	input := `[{"query":"hello","expected_paths":[]}]`

	tests := []testCase{
		{
			name:      "InputSizeSuccess",
			input:     input,
			repeat:    1<<20 - len(input),
			wantError: false,
		},
		{
			name:      "InputSizeFail",
			input:     input,
			repeat:    1<<20 - len(input) + 1,
			wantError: true,
		},
	}

	for _, scene := range tests {
		t.Run(scene.name, func(t *testing.T) {
			data := scene.input + strings.Repeat(" ", scene.repeat)

			cases, err := LoadCases(strings.NewReader(data))

			if scene.wantError {
				if !errors.Is(err, InputLimit) {
					t.Errorf("err=%v, want %v", err, InputLimit)
				}
				if cases != nil {
					t.Errorf("cases=%#v, want nil slice", cases)
				}
			} else {
				if err != nil {
					t.Fatalf("LoadCases() error=%v, want no error", err)
				}
				if len(cases) != 1 {
					t.Errorf("len(cases)=%d, want 1", len(cases))
				}
			}
		})
	}
}
