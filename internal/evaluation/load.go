package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxInputSize = 1 << 20

var InputLimit = errors.New("data is more that 1MiB")

func LoadCases(r io.Reader) ([]Case, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxInputSize+1))
	if err != nil {
		return nil, fmt.Errorf("read cases: %w", err)
	}
	if len(data) > MaxInputSize {
		return nil, InputLimit
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var cases []Case
	decodeErr := decoder.Decode(&cases)
	if decodeErr != nil {
		return nil, fmt.Errorf("decode cases: %w", decodeErr)
	}

	var extra any
	extraErr := decoder.Decode(&extra)
	if extraErr == nil {
		return nil, fmt.Errorf("unexpected JSON value after cases")
	}
	if !errors.Is(extraErr, io.EOF) {
		return nil, fmt.Errorf("invalid data after cases: %w", extraErr)
	}

	validateErr := validateCases(cases)
	if validateErr != nil {
		return nil, fmt.Errorf("validate cases: %w", validateErr)
	}

	return cases, nil
}
