package http

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
)

//go:embed web/index.html
var indexHTML string

type Backend interface {
	search.Engine
	Sources(context.Context) ([]sqlite.SourceStatus, error)
	Evidence(context.Context, string, int) (search.Evidence, error)
}

type options struct {
	syncEnabled bool
	syncStatus  func() []syncer.Status
	ready       func(string) error
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

func Handler(backend Backend, opts ...Option) http.Handler {
	var config options
	for _, option := range opts {
		option(&config)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if !localHost(r.Host) {
			writeError(w, http.StatusForbidden, "local host required")
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
	server := &http.Server{Handler: Handler(backend, opts...), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
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
