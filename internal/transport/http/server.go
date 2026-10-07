package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/sourceapp"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
)

//go:embed web/index.html
var indexHTML string

//go:embed web/citation.mjs
var citationModule []byte

type Backend interface {
	search.Engine
	Sources(context.Context) ([]sqlite.SourceStatus, error)
	Evidence(context.Context, string, int) (search.Evidence, error)
}

type options struct {
	syncEnabled bool
	syncStatus  func() []syncer.Status
	ready       func(string) error
	management  *sourceapp.App
	authority   string
	token       string
}
type Option func(*options)

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

func Handler(backend Backend, opts ...Option) http.Handler {
	var config options
	for _, option := range opts {
		option(&config)
	}
	return handlerWithOptions(backend, config)
}

func handlerWithOptions(backend Backend, config options) http.Handler {
	if config.management != nil && config.authority == "" {
		config.management = nil
	}
	if config.management != nil {
		if config.token == "" {
			var token [32]byte
			if _, err := rand.Read(token[:]); err != nil {
				config.management = nil
			} else {
				config.token = fmt.Sprintf("%x", token[:])
			}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})
	mux.HandleFunc("GET /assets/citation.mjs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(citationModule)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
		}
		request := search.Request{Query: r.URL.Query().Get("q"), SourceID: r.URL.Query().Get("source"), Limit: limit}
		response, err := backend.Search(r.Context(), request)
		if err != nil {
			if errors.Is(err, search.ErrQuery) || errors.Is(err, search.ErrLimit) {
				writeError(w, http.StatusBadRequest, err.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "search unavailable")
			}
			return
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /api/v1/sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := backend.Sources(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "sources unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
	})
	mux.HandleFunc("GET /api/v1/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		page := 0
		if raw := r.URL.Query().Get("page"); raw != "" {
			var err error
			page, err = strconv.Atoi(raw)
			if err != nil || page < 1 {
				writeError(w, 400, "invalid page number")
				return
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
	mux.HandleFunc("GET /api/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		statuses := []syncer.Status{}
		if config.syncStatus != nil {
			statuses = config.syncStatus()
		}
		writeJSON(w, 200, map[string]any{"enabled": config.syncEnabled, "sources": statuses})
	})
	mux.HandleFunc("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"management": config.management != nil})
	})
	if config.management != nil {
		mux.HandleFunc("GET /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Values("Origin")
			if config.token == "" || crossSite(r) || !requestAuthority(r, config.authority) || len(origin) > 1 || (len(origin) == 1 && origin[0] != "http://"+config.authority) {
				writeError(w, 403, "same local origin required")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, map[string]string{"token": config.token})
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
	if len(r.Header.Values("Origin")) != 1 || !requestAuthority(r, c.authority) || crossSite(r) || r.Header.Get("Origin") != "http://"+c.authority {
		writeError(w, 403, "same local origin required")
		return false
	}
	token := r.Header.Get("X-Findrail-Token")
	if len(r.Header.Values("X-Findrail-Token")) != 1 || c.token == "" || len(token) != len(c.token) || subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) != 1 {
		writeError(w, 403, "management token required")
		return false
	}
	if len(r.Header.Values("Content-Type")) != 1 {
		writeError(w, 400, "application/json required")
		return false
	}
	if media := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); media != "application/json" {
		writeError(w, 400, "application/json required")
		return false
	}
	return true
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
	if config.management != nil {
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
func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}
