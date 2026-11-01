// Package ignorepolicy implements Findrail's bounded, root-only ignore language.
package ignorepolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxInput = 64 << 10
const MaxRules = 256
const MaxPatternBytes = 1024
const MaxPatternSegments = 64
const MaxCompiledSegments = 4096
const MaxPathBytes = 4096
const MaxPathSegments = 256
const maxMatchWork = 16 << 20

type Error struct {
	Code string
	Line int
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("ignore policy %s at line %d", e.Code, e.Line)
	}
	return "ignore policy " + e.Code
}

type Rule struct {
	pattern                    string
	display                    string
	line                       int
	negated, dirOnly, anchored bool
	segments                   []string
}
type Policy struct {
	rules       []Rule
	fingerprint string
}
type Decision struct {
	Excluded    bool
	Line        int
	Rule        string
	Ancestor    string
	Fingerprint string
	Reason      string
}

func Parse(ctx context.Context, data []byte) (*Policy, error) {
	if len(data) > MaxInput {
		return nil, &Error{Code: "too_large"}
	}
	if !utf8.Valid(data) {
		return nil, &Error{Code: "invalid_utf8"}
	}
	if err := ctx.Err(); err != nil {
		return nil, ctx.Err()
	}
	data = bytesBOM(data)
	p := &Policy{fingerprint: Fingerprint(data)}
	compiledSegments := 0
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.ContainsRune(text, '\r') {
		return nil, &Error{Code: "invalid_line_ending"}
	}
	for index, r := range text {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if r != '\n' && unicode.IsControl(r) {
			return nil, &Error{Code: "unsupported_character", Line: strings.Count(text[:index], "\n") + 1}
		}
	}
	for i, line := range strings.Split(text, "\n") {
		if i&15 == 0 {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		n := i + 1
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimRight(line, " ")
		if line == "" {
			continue
		}
		display := line
		if hasControl(line) {
			return nil, &Error{Code: "unsupported_character", Line: n}
		}
		neg := false
		if strings.HasPrefix(line, "\\#") || strings.HasPrefix(line, "\\!") {
			line = line[1:]
		} else if strings.HasPrefix(line, "!") {
			neg = true
			line = line[1:]
		}
		if line == "" {
			return nil, &Error{Code: "invalid_pattern", Line: n}
		}
		if len(line) > MaxPatternBytes {
			return nil, &Error{Code: "pattern_too_long", Line: n}
		}
		if strings.Contains(line, "\\") {
			return nil, &Error{Code: "unsupported_escape", Line: n}
		}
		if strings.ContainsAny(line, "[]") {
			return nil, &Error{Code: "unsupported_class", Line: n}
		}
		if uriLike(line) || strings.HasPrefix(line, "/") && strings.HasPrefix(line, "//") || driveLike(line) {
			return nil, &Error{Code: "absolute_pattern", Line: n}
		}
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		dir := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		parts := strings.Split(line, "/")
		if len(parts) > MaxPatternSegments {
			return nil, &Error{Code: "too_many_segments", Line: n}
		}
		for _, s := range parts {
			if s == "" || s == "." || s == ".." {
				return nil, &Error{Code: "invalid_segment", Line: n}
			}
			if strings.Contains(s, "**") && s != "**" {
				return nil, &Error{Code: "invalid_double_star", Line: n}
			}
		}
		if len(p.rules) >= MaxRules {
			return nil, &Error{Code: "too_many_rules", Line: n}
		}
		if compiledSegments+len(parts) > MaxCompiledSegments {
			return nil, &Error{Code: "compiled_limit", Line: n}
		}
		compiledSegments += len(parts)
		p.rules = append(p.rules, Rule{pattern: line, display: display, line: n, negated: neg, dirOnly: dir, anchored: anchored, segments: parts})
	}
	return p, nil
}
func hasControl(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}
func driveLike(s string) bool {
	return len(s) >= 2 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':'
}
func uriLike(s string) bool {
	c := strings.IndexByte(s, ':')
	if c <= 0 {
		return false
	}
	for i, r := range s[:c] {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
		} else if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}
func bytesBOM(b []byte) []byte {
	if len(b) >= 3 && string(b[:3]) == "\xef\xbb\xbf" {
		return b[3:]
	}
	return b
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:16]) }

// Fingerprint returns a bounded, deterministic identity for policy bytes; it is
// not authentication and does not imply that the supplied bytes are valid.
func Fingerprint(data []byte) string { return hash(bytesBOM(data)) }
func (p *Policy) Fingerprint() string {
	if p == nil {
		return ""
	}
	return p.fingerprint
}

func ValidatePath(path string) error {
	if !utf8.ValidString(path) || len(path) > MaxPathBytes || path == "" || strings.ContainsAny(path, "\\\x00") || strings.HasPrefix(path, "/") || strings.Contains(path, "//") || uriLike(path) || driveLike(path) {
		return &Error{Code: "unsafe_path"}
	}
	parts := strings.Split(path, "/")
	if len(parts) > MaxPathSegments {
		return &Error{Code: "too_many_path_segments"}
	}
	for _, s := range parts {
		if s == "" || s == "." || s == ".." {
			return &Error{Code: "unsafe_path"}
		}
		for _, r := range s {
			if unicode.IsControl(r) {
				return &Error{Code: "unsafe_path"}
			}
		}
	}
	return nil
}

func (p *Policy) Decide(ctx context.Context, path string, isDir bool) Decision {
	d := Decision{Fingerprint: p.Fingerprint(), Reason: "eligible"}
	if ValidatePath(path) != nil {
		d.Reason = "invalid_path"
		return d
	}
	if p == nil {
		return d
	}
	parts := strings.Split(path, "/")
	excluded := false
	work := 0
	for _, r := range p.rules {
		if ctx.Err() != nil {
			d.Reason = "canceled"
			return d
		}
		if r.dirOnly && !isDir {
			continue
		}
		charge := (len(r.pattern) + 1) * (len(path) + 1)
		if charge > maxMatchWork-work {
			d.Reason = "matching_limit"
			return d
		}
		work += charge
		matched := matches(ctx, r, parts)
		if ctx.Err() != nil {
			d.Reason = "canceled"
			return d
		}
		if matched {
			excluded = !r.negated
			if excluded {
				d.Line = r.line
				d.Rule = r.display
			} else {
				d.Line = 0
				d.Rule = ""
			}
		}
	}
	if excluded {
		d.Excluded = true
		d.Reason = "user_rule"
	}
	return d
}
func matches(ctx context.Context, r Rule, path []string) bool {
	if len(r.segments) == 1 && !strings.Contains(r.pattern, "/") && !r.anchored {
		for _, s := range path {
			if ctx.Err() != nil {
				return false
			}
			if segment(ctx, r.segments[0], s) {
				return true
			}
		}
		return false
	}
	return globSegments(ctx, r.segments, path)
}
func segment(ctx context.Context, p, s string) bool {
	a, b := []rune(p), []rune(s)
	dp := make([]bool, len(b)+1)
	dp[0] = true
	for _, c := range a {
		if ctx.Err() != nil {
			return false
		}
		next := make([]bool, len(b)+1)
		if c == '*' {
			next[0] = dp[0]
			for j := 1; j <= len(b); j++ {
				next[j] = dp[j] || next[j-1]
			}
		} else {
			for j := 1; j <= len(b); j++ {
				next[j] = dp[j-1] && (c == '?' || c == b[j-1])
			}
		}
		dp = next
	}
	return dp[len(b)]
}
func globSegments(ctx context.Context, p, s []string) bool {
	dp := make([]bool, len(s)+1)
	dp[0] = true
	for _, x := range p {
		if ctx.Err() != nil {
			return false
		}
		n := make([]bool, len(s)+1)
		if x == "**" {
			n[0] = dp[0]
			for j := 1; j <= len(s); j++ {
				n[j] = dp[j] || n[j-1]
			}
		} else {
			for j := 1; j <= len(s); j++ {
				n[j] = dp[j-1] && segment(ctx, x, s[j-1])
			}
		}
		dp = n
	}
	return dp[len(s)]
}
