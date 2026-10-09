package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"
	"unicode"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/google/jsonschema-go/jsonschema"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	DefaultMaxResults    = 10
	DefaultMaxTextChars  = 8000
	MaxAllowedSources    = 16
	MaxSearchResults     = 20
	MaxEvidenceTextChars = 32768
	MaxSnippetChars      = 1024
	MaxResultBytes       = 256 << 10
	MaxFrameBytes        = 64 << 10
)

var supportedProtocolVersions = []string{"2026-07-28", "2025-11-25"}

type Config struct {
	AllowedSources []string
	MaxResults     int
	MaxTextChars   int
	RequestTimeout time.Duration
	Version        string
	Logger         *slog.Logger
}

type Backend interface {
	Search(context.Context, search.Request) (search.Response, error)
	Sources(context.Context) ([]sqlite.SourceStatus, error)
	EvidenceForSource(context.Context, string, string, int) (search.Evidence, error)
}

type Server struct {
	backend        Backend
	allowed        map[string]struct{}
	config         Config
	slots          chan struct{}
	server         *mcp.Server
	resultOverhead int
}

func New(backend Backend, config Config) (*Server, error) {
	if backend == nil {
		return nil, errors.New("MCP backend is required")
	}
	if config.MaxResults == 0 {
		config.MaxResults = DefaultMaxResults
	}
	if config.MaxTextChars == 0 {
		config.MaxTextChars = DefaultMaxTextChars
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Second
	}
	if config.MaxResults < 1 || config.MaxResults > MaxSearchResults {
		return nil, fmt.Errorf("max results must be between 1 and %d", MaxSearchResults)
	}
	if config.MaxTextChars < 256 || config.MaxTextChars > MaxEvidenceTextChars {
		return nil, fmt.Errorf("max text chars must be between 256 and %d", MaxEvidenceTextChars)
	}
	if config.RequestTimeout < time.Second || config.RequestTimeout > 30*time.Second {
		return nil, errors.New("request timeout must be between 1s and 30s")
	}
	allowed := make(map[string]struct{}, len(config.AllowedSources))
	for _, id := range config.AllowedSources {
		if err := validateID(id); err != nil {
			return nil, fmt.Errorf("invalid source: %w", err)
		}
		if _, exists := allowed[id]; exists {
			return nil, fmt.Errorf("duplicate source %q", id)
		}
		allowed[id] = struct{}{}
	}
	if len(allowed) == 0 || len(allowed) > MaxAllowedSources {
		return nil, fmt.Errorf("source allowlist must contain 1 to %d unique IDs", MaxAllowedSources)
	}

	implementation := &mcp.Implementation{Name: "findrail", Version: config.Version}
	// The SDK adds these fields after a handler returns under the new protocol.
	// Reserve their actual serialized size, including the joining comma.
	metadata, err := json.Marshal(map[string]any{
		"_meta":      mcp.Meta{mcp.MetaKeyServerInfo: implementation},
		"resultType": "complete",
	})
	if err != nil {
		return nil, err
	}
	if len(metadata) > MaxResultBytes-1024 {
		return nil, errors.New("MCP server identity exceeds the response budget")
	}
	server := &Server{backend: backend, allowed: allowed, config: config, slots: make(chan struct{}, 4), resultOverhead: len(metadata) - 1}
	server.server = mcp.NewServer(implementation, &mcp.ServerOptions{
		Capabilities:              &mcp.ServerCapabilities{},
		SupportedProtocolVersions: supportedProtocolVersions,
		Logger:                    config.Logger,
	})
	if err := server.addTools(); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *Server) Run(ctx context.Context, transport mcp.Transport) error {
	return s.server.Run(ctx, transport)
}

func (s *Server) addTools() error {
	listInputSchema, err := jsonschema.For[listSourcesInput](nil)
	if err != nil {
		return err
	}
	listOutputSchema, err := jsonschema.For[listSourcesOutput](nil)
	if err != nil {
		return err
	}
	searchInputSchema, err := jsonschema.For[searchInput](nil)
	if err != nil {
		return err
	}
	searchInputSchema.Properties["query"].MinLength = intPtr(1)
	searchInputSchema.Properties["query"].MaxLength = intPtr(256)
	searchInputSchema.Properties["source_id"].MinLength = intPtr(1)
	searchInputSchema.Properties["source_id"].MaxLength = intPtr(128)
	searchInputSchema.Properties["source_id"].Pattern = `^[^\s\p{Z}\p{Cc}]+$`
	searchInputSchema.Properties["limit"].Type = "integer"
	searchInputSchema.Properties["limit"].Types = nil
	searchInputSchema.Properties["limit"].Minimum = floatPtr(1)
	searchInputSchema.Properties["limit"].Maximum = floatPtr(float64(s.config.MaxResults))
	searchInputSchema.Properties["mode"] = &jsonschema.Schema{Type: "string", Enum: []any{"", "literal", "advanced"}, Default: json.RawMessage(`"literal"`), Description: "Literal by default; advanced enables phrases, OR branches, exclusions and token prefixes."}
	searchInputSchema.Properties["format"] = &jsonschema.Schema{Type: "string", Enum: []any{"", "all", "text", "pdf", "docx"}, Default: json.RawMessage(`"all"`)}
	searchInputSchema.Properties["path_prefix"] = &jsonschema.Schema{Type: "string", MaxLength: intPtr(512), Description: "Relative slash-separated indexed path prefix."}
	searchInputSchema.Properties["title_contains"] = &jsonschema.Schema{Type: "string", MaxLength: intPtr(128), Description: "Literal case-sensitive indexed title substring."}
	searchOutputSchema, err := jsonschema.For[searchOutput](nil)
	if err != nil {
		return err
	}
	evidenceInputSchema, err := jsonschema.For[evidenceInput](nil)
	if err != nil {
		return err
	}
	evidenceInputSchema.Properties["source_id"].MinLength = intPtr(1)
	evidenceInputSchema.Properties["source_id"].MaxLength = intPtr(128)
	evidenceInputSchema.Properties["source_id"].Pattern = `^[^\s\p{Z}\p{Cc}]+$`
	evidenceInputSchema.Properties["document_id"].MinLength = intPtr(1)
	evidenceInputSchema.Properties["document_id"].MaxLength = intPtr(128)
	evidenceInputSchema.Properties["document_id"].Pattern = `^[^\s\p{Z}\p{Cc}]+$`
	evidenceInputSchema.Properties["page"].Type = "integer"
	evidenceInputSchema.Properties["page"].Types = nil
	evidenceInputSchema.Properties["page"].Minimum = floatPtr(1)
	evidenceOutputSchema, err := jsonschema.For[evidenceOutput](nil)
	if err != nil {
		return err
	}

	readonly := false
	closedWorld := false
	annotation := func(title string) *mcp.ToolAnnotations {
		return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &readonly, OpenWorldHint: &closedWorld}
	}
	mcp.AddTool[listSourcesInput, any](s.server, &mcp.Tool{
		Name: "findrail_list_sources", Title: "List Findrail sources",
		Description: "List the explicitly permitted, currently registered Findrail sources.",
		Annotations: annotation("List Findrail sources"), InputSchema: listInputSchema, OutputSchema: listOutputSchema,
	}, s.listSources)
	mcp.AddTool[searchInput, any](s.server, &mcp.Tool{
		Name: "findrail_search", Title: "Search indexed Findrail documents",
		Description: "Search one permitted source. Literal mode is the default; optional advanced mode and format/path/title filters follow the shared Findrail query contract.",
		Annotations: annotation("Search indexed Findrail documents"), InputSchema: searchInputSchema, OutputSchema: searchOutputSchema,
	}, s.search)
	mcp.AddTool[evidenceInput, any](s.server, &mcp.Tool{
		Name: "findrail_get_evidence", Title: "Get indexed Findrail evidence",
		Description: "Read a bounded indexed snapshot from one permitted Findrail source, including PDF page provenance.",
		Annotations: annotation("Get indexed Findrail evidence"), InputSchema: evidenceInputSchema, OutputSchema: evidenceOutputSchema,
	}, s.evidence)
	return nil
}

type listSourcesInput struct{}
type searchInput struct {
	Query         string `json:"query"`
	SourceID      string `json:"source_id"`
	Limit         *int   `json:"limit,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Format        string `json:"format,omitempty"`
	PathPrefix    string `json:"path_prefix,omitempty"`
	TitleContains string `json:"title_contains,omitempty"`
}
type evidenceInput struct {
	DocumentID string `json:"document_id"`
	SourceID   string `json:"source_id"`
	Page       *int   `json:"page,omitempty"`
}

type sourceOutput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Documents     int    `json:"documents"`
	LastIndexedAt string `json:"last_indexed_at"`
}
type listSourcesOutput struct {
	Sources []sourceOutput `json:"sources"`
}
type searchOutput struct {
	Query            string         `json:"query"`
	SourceID         string         `json:"source_id"`
	Total            int            `json:"total"`
	Returned         int            `json:"returned"`
	ResultsTruncated bool           `json:"results_truncated"`
	IndexedSnapshot  bool           `json:"indexed_snapshot"`
	ContentUntrusted bool           `json:"content_untrusted"`
	Results          []searchResult `json:"results"`
}
type searchResult struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	URI              string `json:"uri"`
	Path             string `json:"path"`
	SourceID         string `json:"source_id"`
	SourceName       string `json:"source_name"`
	SourceKind       string `json:"source_kind"`
	MediaType        string `json:"media_type"`
	Page             int    `json:"page,omitempty"`
	PageCount        int    `json:"page_count,omitempty"`
	Snippet          string `json:"snippet"`
	SnippetTruncated bool   `json:"snippet_truncated"`
}
type evidenceOutput struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	URI               string   `json:"uri"`
	Path              string   `json:"path"`
	SourceID          string   `json:"source_id"`
	SourceName        string   `json:"source_name"`
	MediaType         string   `json:"media_type"`
	ContentHash       string   `json:"content_hash"`
	ModifiedAt        string   `json:"modified_at"`
	Page              int      `json:"page,omitempty"`
	PageCount         int      `json:"page_count,omitempty"`
	Text              string   `json:"text"`
	TextChars         int      `json:"text_chars"`
	Truncated         bool     `json:"truncated"`
	TruncationReasons []string `json:"truncation_reasons"`
	IndexedSnapshot   bool     `json:"indexed_snapshot"`
	ContentUntrusted  bool     `json:"content_untrusted"`
}

type toolError struct{ Code, Message string }

func (e toolError) Error() string { return e.Code + ": " + e.Message }

func (s *Server) listSources(ctx context.Context, _ *mcp.CallToolRequest, _ listSourcesInput) (*mcp.CallToolResult, any, error) {
	var statuses []sqlite.SourceStatus
	err := s.withBackend(ctx, func(ctx context.Context) error {
		var err error
		statuses, err = s.backend.Sources(ctx)
		return err
	})
	if err != nil {
		return s.errorResult(s.mapBackendError(err, "source list unavailable"))
	}
	result := listSourcesOutput{Sources: make([]sourceOutput, 0, len(statuses))}
	for _, source := range statuses {
		if _, ok := s.allowed[source.ID]; !ok {
			continue
		}
		result.Sources = append(result.Sources, sourceOutput{ID: source.ID, Name: source.Name, Kind: source.Kind, Documents: source.Documents, LastIndexedAt: source.LastIndexedAt})
	}
	sort.Slice(result.Sources, func(i, j int) bool {
		if result.Sources[i].Name == result.Sources[j].Name {
			return result.Sources[i].ID < result.Sources[j].ID
		}
		return result.Sources[i].Name < result.Sources[j].Name
	})
	if len(result.Sources) > MaxAllowedSources {
		result.Sources = result.Sources[:MaxAllowedSources]
	}
	return s.successResult(result)
}

func (s *Server) search(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	request := search.Request{Query: input.Query, SourceID: input.SourceID, Limit: 1, Mode: input.Mode, Format: input.Format, PathPrefix: input.PathPrefix, TitleContains: input.TitleContains}
	if err := search.ValidateRequest(request); err != nil {
		return s.errorResult(toolError{"invalid_arguments", "Search query or filters are invalid."})
	}
	limit := 10
	if limit > s.config.MaxResults {
		limit = s.config.MaxResults
	}
	if input.Limit != nil {
		limit = *input.Limit
	}
	if limit < 1 || limit > s.config.MaxResults {
		return s.errorResult(toolError{"invalid_arguments", "Search limit is outside the configured range."})
	}
	if err := s.ensureSource(ctx, input.SourceID); err != nil {
		return s.errorResult(s.mapBackendError(err, "source unavailable"))
	}
	var response search.Response
	err := s.withBackend(ctx, func(ctx context.Context) error {
		var err error
		request.Limit = limit
		response, err = s.backend.Search(ctx, request)
		return err
	})
	if err != nil {
		return s.errorResult(s.mapBackendError(err, "search unavailable"))
	}
	if response.Total < len(response.Results) || len(response.Results) > limit {
		return s.errorResult(toolError{"backend_unavailable", "Search backend returned an invalid result."})
	}
	result := searchOutput{Query: input.Query, SourceID: input.SourceID, Total: response.Total, IndexedSnapshot: true, ContentUntrusted: true, Results: make([]searchResult, 0, len(response.Results))}
	for _, item := range response.Results {
		if item.ID == "" || item.SourceID != input.SourceID || item.Page < 0 || item.PageCount < 0 || (item.PageCount == 0 && item.Page != 0) || (item.PageCount > 0 && item.Page > item.PageCount) {
			return s.errorResult(toolError{"backend_unavailable", "Search backend returned an invalid result."})
		}
		snippet, truncated := truncateRunes(item.Snippet, MaxSnippetChars)
		result.Results = append(result.Results, searchResult{ID: item.ID, Title: item.Title, URI: item.URI, Path: item.Path, SourceID: item.SourceID, SourceName: item.SourceName, SourceKind: item.SourceKind, MediaType: item.MediaType, Page: item.Page, PageCount: item.PageCount, Snippet: snippet, SnippetTruncated: truncated})
	}
	result.ResultsTruncated = response.Total > len(result.Results)
	result.Returned = len(result.Results)
	return s.fitSearch(result)
}

func (s *Server) evidence(ctx context.Context, _ *mcp.CallToolRequest, input evidenceInput) (*mcp.CallToolResult, any, error) {
	if err := validateID(input.DocumentID); err != nil {
		return s.errorResult(toolError{"invalid_arguments", "Document ID is invalid."})
	}
	page := 0
	if input.Page != nil {
		page = *input.Page
		if page < 1 {
			return s.errorResult(toolError{"invalid_arguments", "Page must be a positive integer."})
		}
	}
	if err := s.ensureSource(ctx, input.SourceID); err != nil {
		return s.errorResult(s.mapBackendError(err, "source unavailable"))
	}
	var evidence search.Evidence
	err := s.withBackend(ctx, func(ctx context.Context) error {
		var err error
		evidence, err = s.backend.EvidenceForSource(ctx, input.SourceID, input.DocumentID, page)
		return err
	})
	if err != nil {
		return s.errorResult(s.mapEvidenceError(err))
	}
	if evidence.ID != input.DocumentID || evidence.SourceID != input.SourceID || evidence.Page < 0 || evidence.PageCount < 0 || (evidence.PageCount == 0 && evidence.Page != 0) || (evidence.PageCount > 0 && evidence.Page > evidence.PageCount) {
		return s.errorResult(toolError{"backend_unavailable", "Evidence backend returned an invalid result."})
	}
	if (page > 0 && evidence.Page != page) || (page == 0 && evidence.PageCount > 0 && evidence.Page != 1) {
		return s.errorResult(toolError{"backend_unavailable", "Evidence backend returned an invalid result."})
	}
	reasons := []string{}
	if evidence.Truncated {
		reasons = append(reasons, "indexed_preview")
	}
	text, textLimited := truncateRunes(evidence.Text, s.config.MaxTextChars)
	if textLimited {
		reasons = append(reasons, "mcp_text_limit")
	}
	result := evidenceOutput{ID: evidence.ID, Title: evidence.Title, URI: evidence.URI, Path: evidence.Path, SourceID: evidence.SourceID, SourceName: evidence.SourceName, MediaType: evidence.MediaType, ContentHash: evidence.ContentHash, ModifiedAt: evidence.ModifiedAt, Page: evidence.Page, PageCount: evidence.PageCount, Text: text, TextChars: len([]rune(text)), Truncated: len(reasons) > 0, TruncationReasons: reasons, IndexedSnapshot: true, ContentUntrusted: true}
	return s.fitEvidence(result)
}

func (s *Server) ensureSource(ctx context.Context, id string) error {
	if _, ok := s.allowed[id]; !ok {
		return toolError{"source_unavailable", "The requested source is unavailable."}
	}
	var sources []sqlite.SourceStatus
	err := s.withBackend(ctx, func(ctx context.Context) error {
		var err error
		sources, err = s.backend.Sources(ctx)
		return err
	})
	if err != nil {
		return s.mapBackendError(err, "source unavailable")
	}
	for _, source := range sources {
		if source.ID == id {
			return nil
		}
	}
	return toolError{"source_unavailable", "The requested source is unavailable."}
}

func (s *Server) withBackend(ctx context.Context, fn func(context.Context) error) error {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return toolError{"server_busy", "The Findrail server is busy; try again."}
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.config.RequestTimeout)
	defer cancel()
	err := fn(requestCtx)
	if errors.Is(requestCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return toolError{"request_timeout", "The Findrail request timed out."}
	}
	if errors.Is(requestCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return toolError{"request_timeout", "The Findrail request was cancelled."}
	}
	return err
}

func (s *Server) mapBackendError(err error, fallback string) toolError {
	var mapped toolError
	if errors.As(err, &mapped) {
		return mapped
	}
	if errors.Is(err, search.ErrQuery) || errors.Is(err, search.ErrLimit) {
		return toolError{"invalid_arguments", "The request arguments are invalid."}
	}
	return toolError{"backend_unavailable", fallback + "."}
}

func (s *Server) mapEvidenceError(err error) toolError {
	if errors.Is(err, search.ErrNotFound) {
		return toolError{"document_unavailable", "Indexed document unavailable in the selected source."}
	}
	if errors.Is(err, search.ErrPage) {
		return toolError{"invalid_page", "The requested page is invalid for this document."}
	}
	return s.mapBackendError(err, "evidence unavailable")
}

func (s *Server) successResult(value any) (*mcp.CallToolResult, any, error) {
	result, err := s.renderResult(value, false)
	if err != nil {
		return s.errorResult(toolError{"response_too_large", "The response is too large."})
	}
	return result, nil, nil
}

func (s *Server) errorResult(err toolError) (*mcp.CallToolResult, any, error) {
	if err.Code == "" {
		err = toolError{"backend_unavailable", "Findrail backend unavailable."}
	}
	value := map[string]any{"error": map[string]string{"code": err.Code, "message": err.Message}}
	result, _ := s.renderResult(value, true)
	result.IsError = true
	return result, nil, nil
}

func (s *Server) fitSearch(result searchOutput) (*mcp.CallToolResult, any, error) {
	for {
		result.Returned = len(result.Results)
		result.ResultsTruncated = result.Total > result.Returned
		wire, err := s.renderResult(result, false)
		if err == nil {
			return wire, nil, nil
		}
		if len(result.Results) <= 1 {
			return s.errorResult(toolError{"response_too_large", "The response is too large."})
		}
		result.Results = result.Results[:len(result.Results)-1]
	}
}

func (s *Server) fitEvidence(result evidenceOutput) (*mcp.CallToolResult, any, error) {
	original := []rune(result.Text)
	if wire, err := s.renderResult(result, false); err == nil {
		return wire, nil, nil
	}
	lo, hi := 0, len(original)
	var best *mcp.CallToolResult
	for lo <= hi {
		mid := lo + (hi-lo)/2
		candidate := result
		candidate.Text = string(original[:mid])
		candidate.TextChars = mid
		candidate.Truncated = true
		candidate.TruncationReasons = appendUnique(candidate.TruncationReasons, "mcp_response_budget")
		wire, err := s.renderResult(candidate, false)
		if err == nil {
			best = wire
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best == nil {
		return s.errorResult(toolError{"response_too_large", "The response is too large."})
	}
	return best, nil, nil
}

func (s *Server) renderResult(value any, isError bool) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: value, IsError: isError}
	wire, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(wire)+s.resultOverhead > MaxResultBytes {
		return nil, errors.New("MCP result exceeds byte budget")
	}
	return result, nil
}

func truncateRunes(value string, max int) (string, bool) {
	runes := []rune(value)
	if len(runes) <= max {
		return value, false
	}
	return string(runes[:max]), true
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func validateID(id string) error {
	runes := []rune(id)
	if len(runes) < 1 || len(runes) > 128 {
		return errors.New("ID must be 1 to 128 Unicode characters")
	}
	for _, r := range runes {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("ID must not contain spaces or control characters")
		}
	}
	return nil
}

func intPtr(value int) *int           { return &value }
func floatPtr(value float64) *float64 { return &value }
