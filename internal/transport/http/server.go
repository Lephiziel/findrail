package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/desktop"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/evidencebundle"
	"github.com/Lephiziel/findrail/internal/originals"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/sourceapp"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
)

//go:embed web/index.html
var indexHTML string

//go:embed web/citation.mjs
var citationModule []byte

//go:embed web/evidence_collection.mjs
var evidenceCollectionModule []byte

//go:embed web/keyboard_navigation.mjs
var keyboardNavigationModule []byte

//go:embed web/search_ui.mjs
var searchUIModule []byte

//go:embed web/preview_ui.mjs
var previewUIModule []byte

//go:embed web/collections_ui.mjs
var collectionsUIModule []byte

//go:embed web/freshness.mjs
var freshnessModule []byte

type Backend interface {
	search.Engine
	Sources(context.Context) ([]sqlite.SourceStatus, error)
	Evidence(context.Context, string, int) (search.Evidence, error)
}

type options struct {
	syncEnabled  bool
	syncStatus   func() []syncer.Status
	ready        func(string) error
	management   *sourceapp.App
	originals    OriginalResolver
	openOriginal func(originals.Action, string) error
	authority    string
	token        string
	desktop      *desktop.Record
	quit         func()
	appVersion   string
}

// WithDesktop enables identity verification and explicit application shutdown.
func WithDesktop(record *desktop.Record, quit func()) Option {
	return func(o *options) { o.desktop, o.quit = record, quit }
}

type collectionBackend interface {
	ListCollections(context.Context) ([]sqlite.Collection, error)
	CreateCollection(context.Context, string) (sqlite.Collection, error)
	LoadCollection(context.Context, string) (sqlite.Collection, error)
	PinCollectionEntry(context.Context, string, int64, string, int, string, bool, string) (sqlite.Collection, error)
	PinCollectionEntryAt(context.Context, string, int64, string, int, string, bool, string, int) (sqlite.Collection, error)
	SaveCollection(context.Context, string, int64, string, []sqlite.CollectionEdit) (sqlite.Collection, error)
	DeleteCollection(context.Context, string, int64) error
}

type evidenceBundleBackend interface {
	ExportEvidenceBundle(context.Context, string) (evidencebundle.Bundle, error)
	PreviewEvidenceBundle(context.Context, evidencebundle.Bundle, map[string]string) (sqlite.BundlePreview, error)
	ImportEvidenceBundle(context.Context, evidencebundle.Bundle, map[string]string, string, string) (sqlite.Collection, error)
}

var errBundleClaim = errors.New("preview claim invalid or expired")

// All HTTP handlers in this process share the bundle memory/admission budget.
var bundleOperationGate = make(chan struct{}, 1)

type evidenceBundleClaim struct {
	Digest   string            `json:"d"`
	Mappings map[string]string `json:"m"`
	Binding  string            `json:"b"`
	Expiry   int64             `json:"e"`
}

func signEvidenceBundleClaim(secret []byte, b evidencebundle.Bundle, mappings map[string]string, binding string, expiry time.Time) (string, error) {
	digest, err := evidencebundle.Digest(b)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(evidenceBundleClaim{Digest: digest, Mappings: mappings, Binding: binding, Expiry: expiry.Unix()})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > 32<<10 {
		return "", sqlite.ErrCollectionLimit
	}
	return token, nil
}

func verifyEvidenceBundleClaim(secret []byte, token string, b evidencebundle.Bundle, now time.Time) (evidenceBundleClaim, error) {
	if len(token) > 32<<10 {
		return evidenceBundleClaim{}, errBundleClaim
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return evidenceBundleClaim{}, errBundleClaim
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return evidenceBundleClaim{}, errBundleClaim
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return evidenceBundleClaim{}, errBundleClaim
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return evidenceBundleClaim{}, errBundleClaim
	}
	var claim evidenceBundleClaim
	if json.Unmarshal(payload, &claim) != nil {
		return evidenceBundleClaim{}, errBundleClaim
	}
	digest, err := evidencebundle.Digest(b)
	if err != nil || claim.Digest != digest || now.Unix() >= claim.Expiry {
		return evidenceBundleClaim{}, errBundleClaim
	}
	return claim, nil
}

type Option func(*options)

func WithDiagnostics(version string) Option { return func(o *options) { o.appVersion = version } }

type OriginalResolver interface {
	ResolveOriginal(context.Context, string, string) (sqlite.OriginalTarget, error)
}

func WithSyncStatus(enabled bool, status func() []syncer.Status) Option {
	return func(o *options) { o.syncEnabled = enabled; o.syncStatus = status }
}

// WithReady runs after the loopback listener is bound, before serving requests.
// It receives the actual URL, including the allocated port when addr uses :0.
// The callback must not wait for a request to the server it is starting.
func WithReady(ready func(string) error) Option {
	return func(o *options) { o.ready = ready }
}

// WithManagement opts one server composition into local source mutations. It is
// deliberately used only by `start`; `serve` and `demo` remain read-only.
func WithManagement(app *sourceapp.App) Option { return func(o *options) { o.management = app } }
func WithOriginals(resolver OriginalResolver, open func(originals.Action, string) error) Option {
	return func(o *options) { o.originals, o.openOriginal = resolver, open }
}

func Handler(backend Backend, opts ...Option) http.Handler {
	var config options
	for _, option := range opts {
		option(&config)
	}
	return handlerWithOptions(backend, config)
}

func handlerWithOptions(backend Backend, config options) http.Handler {
	mux := http.NewServeMux()
	if config.management != nil && config.authority == "" {
		config.management = nil
	}
	if config.management != nil || config.originals != nil {
		if config.token == "" {
			var token [32]byte
			if _, err := rand.Read(token[:]); err != nil {
				config.management, config.originals, config.desktop = nil, nil, nil
			} else {
				config.token = fmt.Sprintf("%x", token[:])
			}
		}
	}
	pageEngine, hasPageEngine := backend.(search.PageEngine)
	collections, hasCollections := backend.(collectionBackend)
	_, hasPassage := backend.(interface {
		Passage(context.Context, string, string, string, int) (search.Evidence, error)
	})
	diagnosticReader, hasDiagnostics := backend.(interface {
		DiagnosticSummary(context.Context) (sqlite.DiagnosticSummary, error)
		SchemaInfo() sqlite.SchemaInfo
	})
	hasDiagnostics = hasDiagnostics && config.appVersion != ""
	if hasDiagnostics {
		mux.HandleFunc("GET /api/v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
			info := diagnosticReader.SchemaInfo()
			actual := info.IndexSchema
			status, code := "ok", "schema_supported"
			if actual > info.CurrentWriteSchema {
				status, code = "error", "schema_unsupported"
			}
			if actual < info.CurrentWriteSchema {
				status, code = "warning", "schema_upgrade_needed"
			}
			counts := map[string]int64{}
			summary, err := diagnosticReader.DiagnosticSummary(r.Context())
			if err != nil {
				status, code = "error", "index_unreadable"
			} else {
				counts = map[string]int64{"sources": int64(summary.Sources), "filesystem_sources": int64(summary.FilesystemSources), "github_sources": int64(summary.GitHubSources), "archive_sources": int64(summary.ArchiveSources), "other_sources": int64(summary.OtherSources), "custom_text_limits": int64(summary.CustomTextLimits), "pdf_enabled": int64(summary.PDFEnabled), "docx_enabled": int64(summary.DOCXEnabled), "docx_disabled": int64(summary.DOCXDisabled)}
			}
			report, buildErr := diagnostics.BuildSupportReport(config.appVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, &actual, info.CurrentWriteSchema, []diagnostics.SupportCheck{{ID: "schema", Status: status, Code: code}}, counts)
			if buildErr != nil {
				writeError(w, 500, "diagnostics unavailable")
				return
			}
			writeJSON(w, 200, report)
		})
	}
	if config.management != nil && hasCollections {
		mux.HandleFunc("GET /api/v1/collections", func(w http.ResponseWriter, r *http.Request) {
			if !collectionReadAllowed(w, r, config) {
				return
			}
			s := collections
			v, e := s.ListCollections(r.Context())
			if e != nil {
				writeError(w, 500, "collections unavailable")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, map[string]any{"collections": v})
		})
		mux.HandleFunc("POST /api/v1/collections", func(w http.ResponseWriter, r *http.Request) {
			var b struct {
				Title string `json:"title"`
			}
			if !mutationAllowed(w, r, config) || !decodeCollectionMutation(w, r, &b) {
				return
			}
			s := collections
			v, e := s.CreateCollection(r.Context(), b.Title)
			if e != nil {
				writeCollectionError(w, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 201, v)
		})
		mux.HandleFunc("GET /api/v1/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
			if !collectionReadAllowed(w, r, config) {
				return
			}
			s := collections
			v, e := s.LoadCollection(r.Context(), r.PathValue("id"))
			if e != nil {
				writeCollectionError(w, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, v)
		})
		mux.HandleFunc("POST /api/v1/collections/{id}/entries", func(w http.ResponseWriter, r *http.Request) {
			var b struct {
				Revision   int64  `json:"revision"`
				DocumentID string `json:"document_id"`
				Page       int    `json:"page"`
				Excerpt    string `json:"excerpt"`
				Whole      bool   `json:"whole_preview"`
				Identity   string `json:"original_identity"`
				TextStart  int    `json:"text_start_char"`
			}
			if !mutationAllowed(w, r, config) || !decodeCollectionMutation(w, r, &b) {
				return
			}
			s := collections
			v, e := s.PinCollectionEntryAt(r.Context(), r.PathValue("id"), b.Revision, b.DocumentID, b.Page, b.Excerpt, b.Whole, b.Identity, b.TextStart)
			if e != nil {
				writeCollectionError(w, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, v)
		})
		mux.HandleFunc("PUT /api/v1/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
			var b struct {
				Revision int64                   `json:"revision"`
				Title    string                  `json:"title"`
				Entries  []sqlite.CollectionEdit `json:"entries"`
			}
			if !mutationAllowed(w, r, config) || !decodeCollectionMutation(w, r, &b) {
				return
			}
			if b.Revision < 1 || b.Title == "" || b.Entries == nil {
				writeError(w, 400, "revision, title, and entries are required")
				return
			}
			s := collections
			v, e := s.SaveCollection(r.Context(), r.PathValue("id"), b.Revision, b.Title, b.Entries)
			if e != nil {
				writeCollectionError(w, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, v)
		})
		mux.HandleFunc("DELETE /api/v1/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
			if !mutationAllowed(w, r, config) || !emptyBody(w, r) {
				return
			}
			if len(r.URL.Query()["revision"]) != 1 {
				writeError(w, 400, "one revision is required")
				return
			}
			rev, e := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
			if e != nil || rev < 1 {
				writeError(w, 400, "revision required")
				return
			}
			s := collections
			if e = s.DeleteCollection(r.Context(), r.PathValue("id"), rev); e != nil {
				writeCollectionError(w, e)
				return
			}
			w.WriteHeader(204)
		})
	}
	bundleBackend, hasBundle := backend.(evidenceBundleBackend)
	bundleEnabled := config.management != nil && hasCollections && hasBundle
	if bundleEnabled {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			bundleEnabled = false
		} else {
			bundleOperation := func(w http.ResponseWriter, r *http.Request, fn func(context.Context) error) {
				select {
				case bundleOperationGate <- struct{}{}:
					defer func() { <-bundleOperationGate }()
				default:
					writeJSON(w, 429, map[string]string{"error": "evidence bundle operation busy", "code": "bundle_busy"})
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				// Context cancellation alone does not interrupt a blocked network
				// body Read. Shorten the connection's read deadline as well.
				controller := http.NewResponseController(w)
				deadline, _ := ctx.Deadline()
				if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
					writeBundleError(w, err)
					return
				}
				defer func() {
					// Keep an expired deadline in force while net/http finishes
					// the response and disposes of an incomplete request body.
					if ctx.Err() == nil && time.Now().Before(deadline) {
						_ = controller.SetReadDeadline(time.Time{})
					}
				}()
				if ctx.Err() == context.Canceled {
					return
				}
				if err := fn(ctx); err != nil {
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						writeJSON(w, 504, map[string]string{"error": "evidence bundle operation timed out", "code": "bundle_timeout"})
					} else if ctx.Err() != context.Canceled {
						writeBundleError(w, err)
					}
				}
			}
			mux.HandleFunc("GET /api/v1/collections/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
				if !collectionReadAllowed(w, r, config) {
					return
				}
				bundleOperation(w, r, func(ctx context.Context) error {
					b, e := bundleBackend.ExportEvidenceBundle(ctx, r.PathValue("id"))
					if e != nil {
						return e
					}
					raw, e := evidencebundle.Encode(b)
					if e != nil {
						return e
					}
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.Header().Set("Content-Disposition", "attachment; filename=\"findrail-evidence-bundle.json\"")
					w.WriteHeader(200)
					_, _ = w.Write(raw)
					return nil
				})
			})
			mux.HandleFunc("POST /api/v1/collections/import/preview", func(w http.ResponseWriter, r *http.Request) {
				requestCtx, requestCancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer requestCancel()
				r = r.WithContext(requestCtx)
				if !mutationAllowed(w, r, config) {
					return
				}
				bundleOperation(w, r, func(ctx context.Context) error {
					var body struct {
						Bundle   json.RawMessage   `json:"bundle"`
						Mappings map[string]string `json:"mappings"`
					}
					if !decodeBundleWrapper(w, r, &body) {
						return nil
					}
					b, e := evidencebundle.Decode(bytes.NewReader(body.Bundle))
					if e != nil {
						writeBundleError(w, e)
						return nil
					}
					p, e := bundleBackend.PreviewEvidenceBundle(ctx, b, body.Mappings)
					if e != nil {
						return e
					}
					if p.Ready {
						p.Claim, e = signEvidenceBundleClaim(secret[:], b, body.Mappings, p.Binding, time.Now().Add(5*time.Minute))
						if e != nil {
							return e
						}
					}
					raw, e := json.Marshal(p)
					if e != nil {
						return e
					}
					if len(raw)+1 > 256<<10 {
						return sqlite.ErrCollectionLimit
					}
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(200)
					_, _ = w.Write(append(raw, '\n'))
					return nil
				})
			})
			mux.HandleFunc("POST /api/v1/collections/import", func(w http.ResponseWriter, r *http.Request) {
				requestCtx, requestCancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer requestCancel()
				r = r.WithContext(requestCtx)
				if !mutationAllowed(w, r, config) {
					return
				}
				bundleOperation(w, r, func(ctx context.Context) error {
					var body struct {
						Bundle json.RawMessage `json:"bundle"`
						Claim  string          `json:"claim"`
						Title  string          `json:"title"`
					}
					if !decodeBundleWrapper(w, r, &body) {
						return nil
					}
					b, e := evidencebundle.Decode(bytes.NewReader(body.Bundle))
					if e != nil {
						writeBundleError(w, e)
						return nil
					}
					claim, claimErr := verifyEvidenceBundleClaim(secret[:], body.Claim, b, time.Now())
					if claimErr != nil {
						return claimErr
					}
					v, e := bundleBackend.ImportEvidenceBundle(ctx, b, claim.Mappings, body.Title, claim.Binding)
					if e != nil {
						return e
					}
					writeJSON(w, 201, v)
					return nil
				})
			})
		}
	}
	if config.originals != nil && (config.management == nil || config.authority == "" || config.openOriginal == nil) {
		config.originals = nil
	}
	if config.desktop != nil && (config.management == nil || config.authority == "" || config.quit == nil) {
		config.desktop = nil
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})
	mux.HandleFunc("GET /assets/citation.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(citationModule)
	})
	mux.HandleFunc("GET /assets/evidence_collection.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(evidenceCollectionModule)
	})
	mux.HandleFunc("GET /assets/keyboard_navigation.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(keyboardNavigationModule)
	})
	mux.HandleFunc("GET /assets/search_ui.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(searchUIModule)
	})
	mux.HandleFunc("GET /assets/preview_ui.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(previewUIModule)
	})
	mux.HandleFunc("GET /assets/collections_ui.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(collectionsUIModule)
	})
	mux.HandleFunc("GET /assets/freshness.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(freshnessModule)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		for _, key := range []string{"q", "source", "limit", "mode", "format", "path_prefix", "title_contains"} {
			if len(values[key]) > 1 {
				writeError(w, 400, "repeated search parameter")
				return
			}
		}
		limit := 20
		if raw := values.Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
		}
		request := search.Request{Query: values.Get("q"), SourceID: values.Get("source"), Limit: limit, Mode: values.Get("mode"), Format: values.Get("format"), PathPrefix: values.Get("path_prefix"), TitleContains: values.Get("title_contains")}
		if err := search.ValidateRequest(request); err != nil {
			var validation *search.ValidationError
			if errors.As(err, &validation) {
				writeJSON(w, 400, map[string]any{"error": validation.Message, "code": validation.Code, "position": validation.Position})
			} else {
				writeError(w, 400, err.Error())
			}
			return
		}
		response, err := backend.Search(r.Context(), request)
		if err != nil {
			var validation *search.ValidationError
			if errors.As(err, &validation) {
				writeJSON(w, 400, map[string]any{"error": validation.Message, "code": validation.Code, "position": validation.Position})
			} else if errors.Is(err, search.ErrQuery) || errors.Is(err, search.ErrLimit) {
				writeError(w, http.StatusBadRequest, err.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "search unavailable")
			}
			return
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /api/v1/search/pages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !hasPageEngine {
			writePageError(w, http.StatusNotFound, "search_page_unavailable", "paged search is unavailable")
			return
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writePageError(w, http.StatusBadRequest, "invalid_search_page_request", "invalid search page parameters")
			return
		}
		allowed := map[string]bool{"q": true, "mode": true, "source": true, "format": true, "path_prefix": true, "title_contains": true, "limit": true, "cursor": true, "offset": true, "page": true}
		for key, vals := range values {
			if !allowed[key] || len(vals) != 1 {
				writePageError(w, http.StatusBadRequest, "invalid_search_page_request", "unknown or repeated search page parameter")
				return
			}
		}
		if _, ok := values["offset"]; ok {
			writePageError(w, http.StatusBadRequest, "invalid_search_page_request", "continue with the signed cursor")
			return
		}
		if _, ok := values["page"]; ok {
			writePageError(w, http.StatusBadRequest, "invalid_search_page_request", "continue with the signed cursor")
			return
		}
		limit := 20
		if raw, ok := values["limit"]; ok {
			limit, err = strconv.Atoi(raw[0])
			if err != nil || limit < 1 || limit > 100 {
				writePageError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
				return
			}
		}
		cursor := values.Get("cursor")
		// Bound the raw opaque value before it reaches the pager's decoder.
		if len(cursor) > 4096 {
			writePageError(w, http.StatusBadRequest, "cursor_invalid", "search cursor is invalid")
			return
		}
		req := search.Request{Query: values.Get("q"), Mode: values.Get("mode"), SourceID: values.Get("source"), Format: values.Get("format"), PathPrefix: values.Get("path_prefix"), TitleContains: values.Get("title_contains"), Limit: limit}
		if err := search.ValidateRequest(req); err != nil {
			writePageFailure(w, err)
			return
		}
		// The optional backend receives the same total budget as the SQLite pager.
		// A shorter caller deadline and cancellation must propagate unchanged.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if r.Context().Err() != nil {
			return
		}
		response, err := pageEngine.SearchPage(ctx, search.PageRequest{Request: req, Cursor: cursor})
		if r.Context().Err() != nil {
			return
		}
		if ctx.Err() != nil {
			writePageFailure(w, ctx.Err())
			return
		}
		if err != nil {
			writePageFailure(w, err)
			return
		}
		body, err := json.Marshal(response)
		if r.Context().Err() != nil {
			return
		}
		if ctx.Err() != nil {
			writePageFailure(w, ctx.Err())
			return
		}
		if err != nil || len(body)+1 > 524288 {
			writePageError(w, http.StatusInternalServerError, "search_page_unavailable", "paged search is unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append(body, '\n'))
	})
	mux.HandleFunc("GET /api/v1/sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := backend.Sources(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "sources unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
	})
	mux.HandleFunc("GET /api/v1/sources/{id}/report", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()["include_paths"]
		if len(v) > 1 || (len(v) == 1 && v[0] != "true" && v[0] != "false") {
			writeError(w, 400, "include_paths must be true or false")
			return
		}
		include := len(v) == 1 && v[0] == "true"
		reader, ok := backend.(interface {
			SourceReport(context.Context, string, bool) (map[string]any, error)
		})
		if !ok {
			writeError(w, 500, "source report unavailable")
			return
		}
		result, err := reader.SourceReport(r.Context(), r.PathValue("id"), include)
		if errors.Is(err, sqlite.ErrSourceNotFound) {
			writeError(w, 404, "source not found")
			return
		}
		if err != nil {
			writeError(w, 500, "source report unavailable")
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/v1/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		page := 0
		if raw := r.URL.Query().Get("page"); raw != "" {
			var err error
			page, err = strconv.Atoi(raw)
			if err != nil || page < 0 {
				writeError(w, 400, "invalid page number")
				return
			}
			if page == 0 {
				page = -1
			}
		}
		evidence, err := backend.Evidence(r.Context(), r.PathValue("id"), page)
		if err != nil {
			switch {
			case errors.Is(err, search.ErrNotFound):
				writeError(w, 404, "indexed document not found")
			case errors.Is(err, search.ErrPage):
				writeError(w, 400, "invalid page number")
			default:
				writeError(w, 500, "preview unavailable")
			}
			return
		}
		writeJSON(w, 200, evidence)
	})
	mux.HandleFunc("GET /api/v1/documents/{id}/passage", func(w http.ResponseWriter, r *http.Request) {
		values, parseErr := url.ParseQuery(r.URL.RawQuery)
		if parseErr != nil {
			writeError(w, 400, "invalid passage parameters")
			return
		}
		for key, vals := range values {
			if key != "q" && key != "start_char" && key != "mode" && key != "original_identity" || len(vals) != 1 {
				writeError(w, 400, "invalid or repeated passage parameter")
				return
			}
		}
		q, startRaw, identity := values.Get("q"), values.Get("start_char"), values.Get("original_identity")
		_, hasQ := values["q"]
		_, hasStart := values["start_char"]
		_, hasMode := values["mode"]
		if identity == "" || hasQ == hasStart || (hasQ && q == "") || (hasStart && hasMode) {
			writeError(w, 400, "identity and exactly one of q or start_char are required")
			return
		}
		if hasMode && values.Get("mode") != "literal" {
			writeError(w, 400, "passage supports literal mode only")
			return
		}
		start := 0
		if hasStart {
			for _, digit := range startRaw {
				if digit < '0' || digit > '9' {
					writeError(w, 400, "invalid start_char")
					return
				}
			}
			n, err := strconv.ParseInt(startRaw, 10, 32)
			if err != nil || n < 0 {
				writeError(w, 400, "invalid start_char")
				return
			}
			start = int(n)
		}
		reader, ok := backend.(interface {
			Passage(context.Context, string, string, string, int) (search.Evidence, error)
		})
		if !ok {
			writeError(w, 404, "passage preview unavailable")
			return
		}
		ev, err := reader.Passage(r.Context(), r.PathValue("id"), q, identity, start)
		if err != nil {
			switch {
			case errors.Is(err, search.ErrNotFound):
				writeError(w, 404, "indexed document not found")
			case errors.Is(err, originals.ErrSnapshotChanged):
				writeError(w, 409, "indexed snapshot changed")
			case errors.Is(err, search.ErrPage), errors.Is(err, search.ErrQuery):
				writeError(w, 400, "invalid passage request")
			default:
				writeError(w, 500, "passage unavailable")
			}
			return
		}
		encoded, encodeErr := json.Marshal(ev)
		if encodeErr != nil || len(encoded)+1 > 512<<10 {
			writeError(w, 500, "passage exceeds response limit")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(append(encoded, '\n'))
	})
	mux.HandleFunc("GET /api/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		statuses := []syncer.Status{}
		if config.syncStatus != nil {
			statuses = config.syncStatus()
		}
		writeJSON(w, 200, map[string]any{"enabled": config.syncEnabled, "sources": statuses})
	})
	mux.HandleFunc("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"management":              config.management != nil,
			"evidence_bundle":         bundleEnabled,
			"portable_snapshots":      config.management != nil,
			"collection_persistence":  config.management != nil && hasCollections,
			"open_originals":          config.originals != nil,
			"desktop":                 config.desktop != nil,
			"client_api_version":      1,
			"search_modes":            []string{"literal", "advanced"},
			"search_filters":          []string{"source", "format", "path_prefix", "title_contains"},
			"pdf_title_only_evidence": true,
			"passage_preview":         hasPassage,
			"search_pagination":       hasPageEngine,
			"diagnostics":             hasDiagnostics,
		})
	})
	if config.originals != nil {
		mux.HandleFunc("POST /api/v1/originals/open", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				DocumentID string           `json:"document_id"`
				Identity   string           `json:"original_identity"`
				Action     originals.Action `json:"action"`
				Page       int              `json:"page,omitempty"`
			}
			if !originalMutationAllowed(w, r, config) || !decodeMutation(w, r, &body) {
				return
			}
			if body.DocumentID == "" || body.Identity == "" || body.Page < 0 || body.Page > 100000 {
				writeError(w, 400, "document, snapshot identity, and valid page are required")
				return
			}
			if body.Action != originals.OpenFile && body.Action != originals.OpenFolder {
				writeError(w, 400, "action must be file or folder")
				return
			}
			if body.Action == originals.OpenFolder && body.Page != 0 {
				writeError(w, 400, "folder action does not accept a page")
				return
			}
			t, err := config.originals.ResolveOriginal(r.Context(), body.DocumentID, body.Identity)
			if err != nil {
				writeOriginalError(w, err)
				return
			}
			if body.Page > 0 {
				if t.MediaType != "application/pdf" {
					writeError(w, 400, "only PDF evidence has pages")
					return
				}
				if _, err = backend.Evidence(r.Context(), body.DocumentID, body.Page); err != nil {
					writeError(w, 400, "page is not present in the indexed snapshot")
					return
				}
			}
			path, err := originals.Resolve(t.Root, t.RelativePath)
			if err != nil {
				writeError(w, 409, "original is missing or no longer safely reachable")
				return
			}
			if body.Action == originals.OpenFile && !originals.AllowedDocument(path) {
				writeError(w, 409, "document type cannot be opened")
				return
			}
			if body.Action == originals.OpenFolder {
				path = filepath.Dir(path)
			}
			// Recheck the registration and filesystem immediately before spawn.
			// This narrows, but cannot eliminate, replacement races with the OS.
			latest, checkErr := config.originals.ResolveOriginal(r.Context(), body.DocumentID, body.Identity)
			if checkErr != nil || latest != t {
				writeError(w, 409, "source registration or snapshot changed before opening")
				return
			}
			currentPath, checkErr := originals.Resolve(t.Root, t.RelativePath)
			if checkErr != nil || (body.Action == originals.OpenFile && currentPath != path) || (body.Action == originals.OpenFolder && filepath.Dir(currentPath) != path) {
				writeError(w, 409, "original changed before opening")
				return
			}
			if err = config.openOriginal(body.Action, path); err != nil {
				writeError(w, 503, "native opener unavailable; open the location manually")
				return
			}
			message := "Opening the current file; preview and saved citation refer to the indexed snapshot."
			if body.Page > 0 {
				message = "Opening the current PDF; the viewer may open at its default page. Preview refers to the indexed snapshot."
			}
			writeJSON(w, 202, map[string]string{"status": "attempted", "message": message})
		})
	}
	if config.desktop != nil && config.management != nil && config.token != "" && config.quit != nil {
		mux.HandleFunc("GET /api/v1/desktop/identity", func(w http.ResponseWriter, r *http.Request) {
			nonces := r.Header.Values("X-Findrail-Desktop-Nonce")
			if !requestAuthority(r, config.authority) || crossSite(r) || len(nonces) != 1 || len(nonces[0]) != len(config.desktop.Nonce) || config.desktop.Nonce == "" || subtle.ConstantTimeCompare([]byte(nonces[0]), []byte(config.desktop.Nonce)) != 1 {
				writeError(w, 403, "private desktop identity required")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, map[string]string{"product": "Findrail", "data_dir": config.desktop.DataDir, "nonce": config.desktop.Nonce})
		})
		mux.HandleFunc("POST /api/v1/desktop/quit", func(w http.ResponseWriter, r *http.Request) {
			if !mutationAllowed(w, r, config) || !decodeMutation(w, r, &struct{}{}) {
				return
			}
			w.WriteHeader(http.StatusAccepted)
			if config.quit != nil {
				time.AfterFunc(100*time.Millisecond, config.quit)
			}
		})
	}
	if config.management != nil || config.originals != nil {
		mux.HandleFunc("GET /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Values("Origin")
			if config.token == "" || crossSite(r) || !requestAuthority(r, config.authority) || len(origin) > 1 || (len(origin) == 1 && origin[0] != "http://"+config.authority) {
				writeError(w, 403, "same local origin required")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, map[string]string{"token": config.token})
		})
	}
	if config.management != nil {
		mux.HandleFunc("POST /api/v1/snapshots/inspect", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Path string `json:"path"`
			}
			if !mutationAllowed(w, r, config) || !decodeSnapshotRequest(w, r, &body, "path") {
				return
			}
			job, err := config.management.InspectSnapshot(body.Path)
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("GET /api/v1/snapshots/inspections/{job_id}", func(w http.ResponseWriter, r *http.Request) {
			if !managementReadAllowed(w, r, config) {
				return
			}
			result := config.management.SnapshotInspection(r.PathValue("job_id"))
			status := http.StatusOK
			if result.Status == "not_found" {
				status = http.StatusNotFound
			}
			if result.Status == "failed" || result.Status == "canceled" {
				status = http.StatusConflict
			}
			if result.Status == "expired" {
				status = http.StatusGone
			}
			writeJSON(w, status, result)
		})
		mux.HandleFunc("POST /api/v1/snapshots/import", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Claim string `json:"claim"`
				Name  string `json:"name"`
			}
			if !mutationAllowed(w, r, config) || !decodeSnapshotRequest(w, r, &body, "claim", "name") {
				return
			}
			job, err := config.management.ImportSnapshot(body.Claim, body.Name)
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("POST /api/v1/sources/{id}/snapshot-export", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OutputPath string `json:"output_path"`
			}
			if !mutationAllowed(w, r, config) || !decodeSnapshotRequest(w, r, &body, "output_path") {
				return
			}
			job, err := config.management.ExportSnapshot(r.PathValue("id"), body.OutputPath)
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("POST /api/v1/sources", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Type         string `json:"type"`
				Path         string `json:"path"`
				Repository   string `json:"repository"`
				Ref          string `json:"ref"`
				Subdirectory string `json:"subdirectory"`
				MaxDOCXBytes *int64 `json:"max_docx_bytes,omitempty"`
			}
			if !mutationAllowed(w, r, config) || !decodeMutation(w, r, &body) {
				return
			}
			var job sourceapp.Job
			var err error
			switch body.Type {
			case "folder":
				maxDOCX := int64(8 << 20)
				if body.MaxDOCXBytes != nil {
					maxDOCX = *body.MaxDOCXBytes
				}
				job, err = config.management.AddFolderWithDOCX(body.Path, maxDOCX)
			case "github":
				job, err = config.management.AddGitHub(body.Repository, body.Ref, body.Subdirectory)
			default:
				writeError(w, 400, "type must be folder or github")
				return
			}
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("POST /api/v1/sources/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
			if !mutationAllowed(w, r, config) || !emptyBody(w, r) {
				return
			}
			job, err := config.management.Refresh(r.PathValue("id"))
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("POST /api/v1/sources/{id}/configure", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				MaxDOCXBytes *int64 `json:"max_docx_bytes"`
			}
			if !mutationAllowed(w, r, config) || !decodeMutation(w, r, &body) {
				return
			}
			if body.MaxDOCXBytes == nil {
				writeError(w, 400, "max_docx_bytes is required")
				return
			}
			job, err := config.management.Configure(r.PathValue("id"), *body.MaxDOCXBytes)
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("DELETE /api/v1/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
			if !mutationAllowed(w, r, config) || !emptyBody(w, r) {
				return
			}
			job, err := config.management.Remove(r.PathValue("id"))
			writeJobResult(w, job, err)
		})
		mux.HandleFunc("GET /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"jobs": config.management.Jobs()})
		})
		mux.HandleFunc("GET /api/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
			job, ok := config.management.Job(r.PathValue("id"))
			if !ok {
				writeError(w, 404, "job not found")
				return
			}
			writeJSON(w, 200, job)
		})
		mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
			if !mutationAllowed(w, r, config) || !emptyBody(w, r) {
				return
			}
			if err := config.management.Cancel(r.PathValue("id")); err != nil {
				writeError(w, 404, "job not found")
				return
			}
			w.WriteHeader(204)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if !localHost(r.Host) {
			writeError(w, http.StatusForbidden, "local host required")
			return
		}
		if len(r.Header.Values("Origin")) > 1 {
			writeError(w, http.StatusForbidden, "same origin required")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "http" || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "same origin required")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func crossSite(r *http.Request) bool {
	values := r.Header.Values("Sec-Fetch-Site")
	if len(values) > 1 {
		return true
	}
	if len(values) == 0 {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(values[0]))
	return value != "same-origin" && value != "same-site"
}
func requestAuthority(r *http.Request, authority string) bool {
	if authority == "" {
		return localHost(r.Host)
	}
	return strings.EqualFold(r.Host, authority)
}
func mutationAllowed(w http.ResponseWriter, r *http.Request, c options) bool {
	if c.management == nil {
		writeError(w, 404, "management unavailable")
		return false
	}
	return mutationAuthAllowed(w, r, c)
}

func managementReadAllowed(w http.ResponseWriter, r *http.Request, c options) bool {
	if c.management == nil {
		writeError(w, 404, "management unavailable")
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && origins[0] != "http://"+c.authority || !requestAuthority(r, c.authority) || crossSite(r) {
		writeError(w, 403, "same local origin required")
		return false
	}
	token := r.Header.Get("X-Findrail-Token")
	if len(r.Header.Values("X-Findrail-Token")) != 1 || c.token == "" || len(token) != len(c.token) || subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) != 1 {
		writeError(w, 403, "session token required")
		return false
	}
	return true
}

func decodeSnapshotRequest(w http.ResponseWriter, r *http.Request, target any, required ...string) bool {
	const limit = 32 << 10
	if r.ContentLength > limit {
		writeError(w, 413, "request too large")
		return false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(b) > limit {
		writeError(w, 413, "request too large")
		return false
	}
	if !utf8.Valid(b) || !jsonObjectsUnique(b) || !jsonUnicodeScalarsValid(b) {
		writeError(w, 400, "invalid snapshot request")
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		writeError(w, 400, "invalid snapshot request")
		return false
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeError(w, 400, "required snapshot request field missing")
			return false
		}
	}
	if len(fields) != len(required) {
		writeError(w, 400, "unknown snapshot request field")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		writeError(w, 400, "invalid snapshot request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid snapshot request")
		return false
	}
	return true
}

func originalMutationAllowed(w http.ResponseWriter, r *http.Request, c options) bool {
	if c.originals == nil {
		writeError(w, 404, "native opener unavailable")
		return false
	}
	return mutationAuthAllowed(w, r, c)
}

func mutationAuthAllowed(w http.ResponseWriter, r *http.Request, c options) bool {
	if len(r.Header.Values("Origin")) != 1 || !requestAuthority(r, c.authority) || crossSite(r) || r.Header.Get("Origin") != "http://"+c.authority {
		writeError(w, 403, "same local origin required")
		return false
	}
	token := r.Header.Get("X-Findrail-Token")
	if len(r.Header.Values("X-Findrail-Token")) != 1 || c.token == "" || len(token) != len(c.token) || subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) != 1 {
		writeError(w, 403, "session token required")
		return false
	}
	if len(r.Header.Values("Content-Type")) != 1 || strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])) != "application/json" {
		writeError(w, 400, "application/json required")
		return false
	}
	return true
}

func writeOriginalError(w http.ResponseWriter, err error) {
	if errors.Is(err, search.ErrNotFound) {
		writeError(w, 404, "indexed document not found")
		return
	}
	if errors.Is(err, originals.ErrSnapshotChanged) || errors.Is(err, originals.ErrNotFilesystem) {
		writeError(w, 409, "indexed source or snapshot changed; refresh search and select the current result")
		return
	}
	writeError(w, 500, "current source provenance is unavailable")
}
func decodeMutation(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.ContentLength > 16<<10 {
		writeError(w, 413, "request too large")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
	if err != nil {
		writeError(w, 400, "invalid request")
		return false
	}
	if len(body) > 16<<10 {
		writeError(w, 413, "request too large")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, 400, "invalid request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, 400, "invalid request")
		return false
	}
	return true
}
func emptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > 0 {
		writeError(w, 400, "request body must be empty")
		return false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(b) > 0 {
		writeError(w, 400, "request body must be empty")
		return false
	}
	return true
}
func writeJobResult(w http.ResponseWriter, job sourceapp.Job, err error) {
	if err != nil {
		switch err.Error() {
		case "busy":
			writeError(w, 409, "target is busy")
		case "queue_full":
			writeError(w, 429, "job queue is full")
		case "shutdown":
			writeError(w, 503, "server is shutting down")
		case "not_found":
			writeError(w, 404, "source not found")
		case "already_added":
			writeError(w, 409, "source is already added; use Refresh in Sources")
		case "invalid_claim":
			writeError(w, 409, "snapshot claim is invalid, expired, or already used")
		case "snapshot_preview_busy":
			writeError(w, 409, "target is busy")
		case "invalid_path", "invalid_name":
			writeError(w, 400, "invalid snapshot path or name")
		default:
			writeError(w, 400, boundedHTTPError(err.Error()))
		}
		return
	}
	writeJSON(w, 202, map[string]any{"job": job})
}
func boundedHTTPError(s string) string {
	if len(s) > 256 {
		s = s[:256]
	}
	return s
}

func decodeCollectionMutation(w http.ResponseWriter, r *http.Request, target any) bool {
	const maxCollectionWrite = 4 << 20
	if r.ContentLength > maxCollectionWrite {
		writeError(w, 413, "collection write too large")
		return false
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, maxCollectionWrite+1))
	if e != nil || len(b) > maxCollectionWrite {
		writeError(w, 413, "collection write too large")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		writeError(w, 400, "invalid collection request")
		return false
	}
	var x any
	if d.Decode(&x) != io.EOF {
		writeError(w, 400, "invalid collection request")
		return false
	}
	return true
}

func decodeBundleWrapper(w http.ResponseWriter, r *http.Request, target any) bool {
	const limit = 5 << 20
	if r.ContentLength > limit {
		writeError(w, 413, "request too large")
		return false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	var timeout net.Error
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		writeJSON(w, 504, map[string]string{"error": "evidence bundle operation timed out", "code": "bundle_timeout"})
		return false
	}
	if len(b) > limit {
		writeError(w, 413, "request too large")
		return false
	}
	if err != nil {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	if r.Context().Err() == context.Canceled {
		return false
	}
	if !utf8.Valid(b) || !jsonObjectsUnique(b) || !jsonUnicodeScalarsValid(b) {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	for key := range fields {
		if key != "bundle" && key != "mappings" && key != "claim" && key != "title" {
			writeError(w, 400, "invalid bundle request")
			return false
		}
	}
	if fields["bundle"] == nil {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	if fields["mappings"] == nil && fields["claim"] == nil {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	if fields["mappings"] != nil && (len(fields["mappings"]) == 0 || fields["mappings"][0] != '{') {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	if fields["claim"] == nil && fields["mappings"] == nil || fields["claim"] != nil && (fields["title"] == nil || fields["mappings"] != nil) {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid bundle request")
		return false
	}
	return true
}

// JSON syntax has already been validated. Preserve scalar values instead of
// allowing encoding/json to replace an unpaired escaped surrogate with U+FFFD.
func jsonUnicodeScalarsValid(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func writeBundleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, evidencebundle.ErrLimit), errors.Is(err, sqlite.ErrCollectionLimit):
		writeError(w, 413, "evidence bundle or destination collection exceeds its limits")
	case errors.Is(err, evidencebundle.ErrInvalid):
		writeError(w, 400, "invalid evidence bundle or source mappings")
	case errors.Is(err, sqlite.ErrCollectionNotFound), errors.Is(err, search.ErrNotFound):
		writeError(w, 404, "saved collection or indexed document not found")
	case errors.Is(err, errBundleClaim), errors.Is(err, sqlite.ErrCollectionSnapshotChanged), errors.Is(err, sqlite.ErrCollectionConflict):
		writeError(w, 409, "preview expired or indexed data changed; preview the bundle again")
	default:
		writeError(w, 500, "evidence bundle operation unavailable")
	}
}

func jsonObjectsUnique(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func() bool
	value = func() bool {
		t, e := d.Token()
		if e != nil {
			return false
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return false
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !value() {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value() {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value() {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
func collectionReadAllowed(w http.ResponseWriter, r *http.Request, c options) bool {
	t := r.Header.Get("X-Findrail-Token")
	origins := r.Header.Values("Origin")
	if c.management == nil || c.token == "" || len(origins) > 1 || (len(origins) == 1 && origins[0] != "http://"+c.authority) || !requestAuthority(r, c.authority) || crossSite(r) || len(r.Header.Values("X-Findrail-Token")) != 1 || len(t) != len(c.token) || subtle.ConstantTimeCompare([]byte(t), []byte(c.token)) != 1 {
		writeError(w, 403, "authenticated same local origin required")
		return false
	}
	return true
}
func writeCollectionError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, sqlite.ErrCollectionNotFound), errors.Is(e, search.ErrNotFound):
		writeError(w, 404, e.Error())
	case errors.Is(e, sqlite.ErrCollectionSnapshotChanged):
		writeError(w, 409, e.Error())
	case errors.Is(e, errBundleClaim):
		writeError(w, 409, "preview claim expired or is invalid")
	case errors.Is(e, sqlite.ErrCollectionConflict):
		writeError(w, 409, "collection revision conflict")
	case errors.Is(e, sqlite.ErrCollectionLimit):
		writeError(w, 413, e.Error())
	default:
		writeError(w, 400, boundedHTTPError(e.Error()))
	}
}

func localHost(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateAddress checks the local serving boundary without opening a listener.
func ValidateAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !localHost(host) {
		return fmt.Errorf("server address must use localhost or a loopback IP with a port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("server port must be between 0 and 65535")
	}
	return nil
}

func Serve(ctx context.Context, addr string, backend Backend, opts ...Option) error {
	if err := ValidateAddress(addr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	var config options
	for _, option := range opts {
		option(&config)
	}
	if config.management != nil || config.originals != nil {
		config.authority = listener.Addr().String()
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		config.token = fmt.Sprintf("%x", b[:])
	}
	server := &http.Server{Handler: handlerWithOptions(backend, config), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	if config.ready != nil {
		if err := config.ready("http://" + listener.Addr().String()); err != nil {
			return err
		}
	}
	finished := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				server.Close()
			}
		case <-finished:
		}
	}()
	err = server.Serve(listener)
	close(finished)
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

func writePageError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}

func writePageFailure(w http.ResponseWriter, err error) {
	var validation *search.ValidationError
	if errors.As(err, &validation) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": validation.Message, "code": validation.Code, "position": validation.Position})
		return
	}
	status, code, message := pageHTTPError(err)
	writePageError(w, status, code, message)
}

func pageHTTPError(err error) (int, string, string) {
	switch {
	case errors.Is(err, search.ErrCursorInvalid):
		return http.StatusBadRequest, "cursor_invalid", "search cursor is invalid"
	case errors.Is(err, search.ErrCursorMismatch):
		return http.StatusBadRequest, "cursor_mismatch", "search cursor does not match this request"
	case errors.Is(err, search.ErrCursorExpired):
		return http.StatusConflict, "cursor_expired", "search cursor expired; restart the search"
	case errors.Is(err, search.ErrCursorStale):
		return http.StatusConflict, "cursor_stale", "indexed data changed; restart the search"
	case errors.Is(err, search.ErrCursorSessionChanged):
		return http.StatusConflict, "cursor_session_changed", "search cursor belongs to another server session"
	case errors.Is(err, search.ErrPageBusy):
		return http.StatusTooManyRequests, "search_page_busy", "paged search is busy"
	case errors.Is(err, search.ErrPageClosed), errors.Is(err, search.ErrPageUnavailable):
		return http.StatusServiceUnavailable, "search_page_unavailable", "paged search is unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "search_page_timeout", "paged search timed out"
	case errors.Is(err, search.ErrPageResultTooLarge):
		return http.StatusInternalServerError, "page_result_too_large", "a search result exceeds the response limit"
	case errors.Is(err, search.ErrLimit):
		return http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100"
	case errors.Is(err, search.ErrQuery):
		return http.StatusBadRequest, "invalid_search_page_request", "invalid search query"
	default:
		return http.StatusInternalServerError, "search_page_unavailable", "paged search is unavailable"
	}
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}
