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
)

//go:embed web/index.html
var indexHTML string

type Backend interface {
	search.Engine
	Sources(context.Context) ([]sqlite.SourceStatus, error)
}

func Handler(backend Backend) http.Handler {
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

func Serve(ctx context.Context, addr string, backend Backend) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !localHost(host) {
		return fmt.Errorf("server address must use localhost or a loopback IP with a port")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: Handler(backend), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	finished := make(chan struct{})
	defer close(finished)
	go func() {
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
