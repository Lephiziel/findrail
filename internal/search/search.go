package search

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

var ErrQuery = errors.New("query must contain letters or numbers and be at most 256 characters")
var ErrLimit = errors.New("limit must be between 1 and 100")
var ErrNotFound = errors.New("indexed document not found")
var ErrPage = errors.New("invalid page number")

type Request struct {
	Query    string
	SourceID string
	Limit    int
}

type Result struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	URI        string  `json:"uri"`
	Path       string  `json:"path"`
	SourceID   string  `json:"source_id"`
	SourceName string  `json:"source_name"`
	SourceKind string  `json:"source_kind"`
	Snippet    string  `json:"snippet"`
	Score      float64 `json:"score"`
	MediaType  string  `json:"media_type"`
	Page       int     `json:"page,omitempty"`
	PageCount  int     `json:"page_count,omitempty"`
}

// Evidence is an indexed snapshot, never an arbitrary read from the filesystem.
type Evidence struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URI         string `json:"uri"`
	Path        string `json:"path"`
	SourceID    string `json:"source_id"`
	SourceName  string `json:"source_name"`
	MediaType   string `json:"media_type"`
	ContentHash string `json:"content_hash"`
	ModifiedAt  string `json:"modified_at"`
	Page        int    `json:"page,omitempty"`
	PageCount   int    `json:"page_count,omitempty"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

type Response struct {
	Query   string   `json:"query"`
	Total   int      `json:"total"`
	Results []Result `json:"results"`
}

type Engine interface {
	Search(context.Context, Request) (Response, error)
}

// Expression turns user text into literal AND-connected FTS terms. SQLite query
// operators are not exposed in this initial query language.
func Expression(query string) (string, error) {
	if len([]rune(query)) > 256 {
		return "", ErrQuery
	}
	terms := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(terms) == 0 {
		return "", ErrQuery
	}
	for i, term := range terms {
		terms[i] = `"` + term + `"`
	}
	return strings.Join(terms, " AND "), nil
}
