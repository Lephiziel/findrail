package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"example.com/findrail-catalog"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/collections/demo/resources", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"collection": "demo", "snapshot": "demo-1", "complete": true, "items": []any{map[string]any{"id": "welcome", "title": "Synthetic welcome", "path": "welcome.txt", "uri": "https://catalog.example/welcome", "body": "A synthetic searchable catalogue entry.", "modified": "2026-01-01T00:00:00Z"}}})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Shutdown(context.Background())
	adapter, err := catalog.New(catalog.Config{Origin: "http://" + listener.Addr().String(), Collection: "demo", AllowLoopbackHTTP: true})
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stdout, "source %s (%s)\n", adapter.Source().ID, adapter.Source().Kind)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := adapter.Scan(ctx, func(d connector.Document) error {
		out := struct{ ID, Title, URI, Path, Hash, Content string }{d.ID, d.Title, d.URI, d.Path, d.Hash, d.Content}
		return json.NewEncoder(os.Stdout).Encode(out)
	})
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stdout, "complete report: seen=%d skipped=%d\n", report.Seen, report.Skipped)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "catalog demo:", err); os.Exit(1) }
