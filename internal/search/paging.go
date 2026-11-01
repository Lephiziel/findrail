package search

import (
	"context"
	"errors"
)

// PageWindowLimit bounds the private position space available to a paged query.
const PageWindowLimit = 1000

// PageRequest carries the unchanged search tuple and an opaque continuation
// cursor. Position and consistency state are only present in the signed cursor.
type PageRequest struct {
	Request Request
	Cursor  string
}

// PageEngine is an optional capability and deliberately does not extend Engine.
type PageEngine interface {
	SearchPage(context.Context, PageRequest) (PageResponse, error)
}

var (
	ErrCursorInvalid        = errors.New("invalid search cursor")
	ErrCursorMismatch       = errors.New("search cursor does not match request")
	ErrCursorExpired        = errors.New("search cursor expired")
	ErrCursorStale          = errors.New("search results changed")
	ErrCursorSessionChanged = errors.New("search cursor session changed")
	ErrPageBusy             = errors.New("search page capacity exceeded")
	ErrPageClosed           = errors.New("search pager closed")
	ErrPageUnavailable      = errors.New("search pager unavailable")
	ErrPageResultTooLarge   = errors.New("search result too large")
)

// PageResult is a paged search result. The regular Result remains unchanged.
type PageResult struct {
	Result
	SnippetTruncated bool `json:"snippet_truncated"`
}

// PageResponse describes one bounded page, limited to the first 1,000 matches.
type PageResponse struct {
	Results     []PageResult `json:"results"`
	Total       int          `json:"total"`
	Offset      int          `json:"offset"`
	Returned    int          `json:"returned"`
	Limit       int          `json:"limit"`
	WindowLimit int          `json:"window_limit"`
	NextCursor  *string      `json:"next_cursor"`
	EndReason   *string      `json:"end_reason"`
}
