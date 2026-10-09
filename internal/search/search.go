package search

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrQuery = errors.New("query must contain letters or numbers and be at most 256 characters")
var ErrLimit = errors.New("limit must be between 1 and 100")
var ErrNotFound = errors.New("indexed document not found")
var ErrPage = errors.New("invalid page number")

type Request struct {
	Query         string
	SourceID      string
	Limit         int
	Mode          string
	Format        string
	PathPrefix    string
	TitleContains string
}

type Atom struct {
	Text                    string
	Phrase, Prefix, Exclude bool
}
type Branch []Atom
type Plan []Branch

// ValidationError is safe to return to clients; Position is a zero-based Unicode code-point offset.
type ValidationError struct {
	Code     string
	Position int
	Message  string
}

func (e *ValidationError) Error() string { return e.Message }

func validation(code string, pos int, message string) error {
	return &ValidationError{Code: code, Position: pos, Message: message}
}

// ParseAdvanced parses Findrail's deliberately restricted query language without exposing FTS syntax.
func ParseAdvanced(q string) (Plan, error) {
	if !utf8.ValidString(q) {
		return nil, validation("syntax", 0, "query must be valid UTF-8")
	}
	if utf8.RuneCountInString(q) > 256 {
		return nil, validation("query_too_large", 256, "query must be valid UTF-8 and at most 256 characters")
	}
	rs := []rune(q)
	plan := Plan{}
	branch := Branch{}
	atoms := 0
	afterOR := false
	for i := 0; i < len(rs); {
		r := rs[i]
		if unicode.IsSpace(r) {
			i++
			continue
		}
		if unicode.IsControl(r) {
			return nil, validation("syntax", i, "control characters are not allowed")
		}
		if r == '(' || r == ')' || r == ':' || r == '^' || r == '+' {
			return nil, validation("syntax", i, "unsupported query character")
		}
		if r == '-' {
			if i+1 < len(rs) && unicode.IsSpace(rs[i+1]) {
				return nil, validation("syntax", i, "minus must directly precede an atom")
			}
			// handled as part of atom below
		}
		start := i
		excluded := false
		if rs[i] == '-' {
			excluded = true
			i++
			if i >= len(rs) || rs[i] == '-' {
				return nil, validation("syntax", start, "invalid exclusion")
			}
		}
		atom := Atom{Exclude: excluded}
		if rs[i] == '"' {
			i++
			var b strings.Builder
			for i < len(rs) && rs[i] != '"' {
				if rs[i] == '\r' || rs[i] == '\n' || unicode.IsControl(rs[i]) {
					return nil, validation("syntax", i, "control characters and line breaks are not allowed in phrases")
				}
				if rs[i] == '\\' {
					i++
					if i >= len(rs) || (rs[i] != '\\' && rs[i] != '"') {
						return nil, validation("syntax", i, "only \\\" and \\\\ escapes are supported")
					}
				}
				b.WriteRune(rs[i])
				i++
			}
			if i >= len(rs) {
				return nil, validation("syntax", start, "unclosed phrase")
			}
			i++
			atom.Text = b.String()
			atom.Phrase = true
			hasToken := false
			for _, rr := range atom.Text {
				if unicode.IsLetter(rr) || unicode.IsNumber(rr) {
					hasToken = true
					break
				}
			}
			if !hasToken {
				return nil, validation("syntax", start, "phrase must contain searchable tokens")
			}
		} else {
			var b strings.Builder
			hasBase := false
			for i < len(rs) && (unicode.IsLetter(rs[i]) || unicode.IsNumber(rs[i]) || unicode.IsMark(rs[i]) || rs[i] == '*') {
				if unicode.IsLetter(rs[i]) || unicode.IsNumber(rs[i]) {
					hasBase = true
				}
				if unicode.IsMark(rs[i]) && !hasBase {
					return nil, validation("syntax", i, "combining marks must follow a letter or number")
				}
				b.WriteRune(rs[i])
				i++
			}
			if b.Len() == 0 {
				return nil, validation("syntax", i, "expected a term or quoted phrase")
			}
			atom.Text = b.String()
			if strings.Contains(atom.Text, "*") {
				if !strings.HasSuffix(atom.Text, "*") || strings.Count(atom.Text, "*") != 1 {
					return nil, validation("syntax", start, "only one trailing wildcard is supported")
				}
				atom.Prefix = true
				atom.Text = strings.TrimSuffix(atom.Text, "*")
				n := 0
				for _, x := range atom.Text {
					if unicode.IsLetter(x) || unicode.IsNumber(x) {
						n++
					}
				}
				if n < 3 {
					return nil, validation("syntax", start, "prefix needs at least three letters or numbers")
				}
			}
			upper := atom.Text
			leftBoundary := start == 0 || unicode.IsSpace(rs[start-1])
			rightBoundary := i == len(rs) || unicode.IsSpace(rs[i])
			if upper == "OR" && !excluded && leftBoundary && rightBoundary {
				if len(branch) == 0 {
					return nil, validation("syntax", start, "OR needs a left branch")
				}
				plan = append(plan, branch)
				branch = nil
				afterOR = true
				if len(plan) >= 8 {
					return nil, validation("query_too_large", start, "query may contain at most eight OR branches")
				}
				i++
				continue
			}
			if upper == "AND" || upper == "NOT" || upper == "NEAR" {
				return nil, validation("syntax", start, "reserved operator; quote it to search literally")
			}
		}
		if atom.Phrase && i < len(rs) && rs[i] == '*' {
			return nil, validation("syntax", i, "phrases cannot use a wildcard")
		}
		atoms++
		afterOR = false
		if atoms > 32 {
			return nil, validation("query_too_large", start, "query may contain at most 32 atoms")
		}
		branch = append(branch, atom)
	}
	if len(branch) > 0 {
		plan = append(plan, branch)
	}
	if afterOR {
		return nil, validation("syntax", len(rs), "OR needs a right branch")
	}
	if len(plan) == 0 {
		return nil, validation("syntax", 0, "query contains no searchable terms")
	}
	for _, b := range plan {
		positive := false
		for _, a := range b {
			if !a.Exclude {
				positive = true
			}
		}
		if !positive {
			return nil, validation("missing_positive_term", 0, "each OR branch needs a positive term")
		}
	}
	return plan, nil
}

func ValidateRequest(r Request) error {
	if r.Mode != "" && r.Mode != "literal" && r.Mode != "advanced" {
		return validation("invalid_mode", 0, "mode must be literal or advanced")
	}
	if r.Mode == "advanced" {
		if _, err := ParseAdvanced(r.Query); err != nil {
			return err
		}
	} else {
		if utf8.RuneCountInString(r.Query) > 256 {
			return ErrQuery
		}
		if _, err := Expression(r.Query); err != nil {
			return err
		}
	}
	if r.Format != "" && r.Format != "all" && r.Format != "text" && r.Format != "pdf" && r.Format != "docx" {
		return validation("invalid_filter", 0, "format must be all, text, pdf, or docx")
	}
	p := r.PathPrefix
	if len(p) > 2048 || utf8.RuneCountInString(p) > 512 {
		return validation("invalid_filter", 0, "path prefix is too long")
	}
	if p != "" {
		if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) || !utf8.ValidString(p) || (len(p) >= 2 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':') {
			return validation("invalid_filter", 0, "path prefix must be a relative slash path")
		}
		p = strings.TrimSuffix(p, "/")
		for _, s := range strings.Split(p, "/") {
			if s == "" || s == "." || s == ".." {
				return validation("invalid_filter", 0, "path prefix contains an invalid segment")
			}
			for _, c := range s {
				if unicode.IsControl(c) {
					return validation("invalid_filter", 0, "path prefix contains a control character")
				}
			}
		}
	}
	if !utf8.ValidString(r.TitleContains) || len(r.TitleContains) > 512 || utf8.RuneCountInString(r.TitleContains) > 128 {
		return validation("invalid_filter", 0, "title filter is too long")
	}
	for _, c := range r.TitleContains {
		if unicode.IsControl(c) {
			return validation("invalid_filter", 0, "title filter contains a control character")
		}
	}
	return nil
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
	SourceKind  string `json:"source_kind"`
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
