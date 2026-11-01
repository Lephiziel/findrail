package http

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/evidencebundle"
	"github.com/Lephiziel/findrail/internal/sourceapp"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type bundleContractBackend struct {
	*sqlite.Store
	export func(context.Context) (evidencebundle.Bundle, error)
}

func (b bundleContractBackend) ExportEvidenceBundle(ctx context.Context, _ string) (evidencebundle.Bundle, error) {
	return b.export(ctx)
}

func bundleContractServer(t *testing.T, backend Backend, timeout time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	h := handlerWithOptions(backend, options{management: &sourceapp.App{}, authority: srv.Listener.Addr().String(), token: "synthetic-token"})
	srv.Config.Handler = nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if timeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		h.ServeHTTP(w, r)
	})
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func getContractBundle(srv *httptest.Server) (*nethttp.Response, error) {
	r, _ := nethttp.NewRequest(nethttp.MethodGet, srv.URL+"/api/v1/collections/synthetic/bundle", nil)
	r.Header.Set("X-Findrail-Token", "synthetic-token")
	return srv.Client().Do(r)
}

func TestBundleErrorsDoNotExposeBackendDetails(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := bundleContractServer(t, bundleContractBackend{Store: s, export: func(context.Context) (evidencebundle.Bundle, error) {
		return evidencebundle.Bundle{}, errors.New("private excerpt and file:///private/original.md from SQL diagnostic")
	}}, 0)
	response, err := getContractBundle(srv)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 500 || strings.Contains(string(body), "private") || strings.Contains(string(body), "SQL") {
		t.Fatalf("bundle error leaked backend details: status=%d body=%s", response.StatusCode, body)
	}
}

func TestBundleAdmissionIsSharedAcrossHandlers(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	backend := bundleContractBackend{Store: s, export: func(ctx context.Context) (evidencebundle.Bundle, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return evidencebundle.Bundle{}, ctx.Err()
			}
		}
		return evidencebundle.Bundle{Title: "Synthetic bundle"}, nil
	}}
	first, second := bundleContractServer(t, backend, 0), bundleContractServer(t, backend, 0)
	done := make(chan error, 1)
	go func() {
		response, err := getContractBundle(first)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	<-entered
	response, err := getContractBundle(second)
	once.Do(func() { close(release) })
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 429 || calls.Load() != 1 {
		t.Fatalf("concurrent bundle operations admitted by two handlers: status=%d calls=%d", response.StatusCode, calls.Load())
	}
}

func TestBundleBodyReadHonorsShorterRequestDeadline(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv := bundleContractServer(t, s, 80*time.Millisecond)
	conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST /api/v1/collections/import/preview HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nX-Findrail-Token: synthetic-token\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{", srv.Listener.Addr(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := nethttp.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("bundle body read outlived the request deadline: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 504 {
		t.Fatalf("partial request body returned status %d", response.StatusCode)
	}
}

func TestBundleWrapperRejectsUnpairedSurrogates(t *testing.T) {
	for _, escaped := range []string{`\ud800`, `\udc00`, `\ud800x`} {
		var target struct {
			Bundle   any               `json:"bundle"`
			Mappings map[string]string `json:"mappings"`
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(nethttp.MethodPost, "/", strings.NewReader(`{"bundle":{},"mappings":{"s1":"`+escaped+`"}}`))
		if decodeBundleWrapper(w, r, &target) || w.Code != 400 {
			t.Fatalf("malformed Unicode scalar accepted in wrapper: %s", escaped)
		}
	}
	for _, escaped := range []string{`\ud83d\ude00`, `\\ud800`, `intentional \ufffd`} {
		var target struct {
			Bundle   any               `json:"bundle"`
			Mappings map[string]string `json:"mappings"`
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(nethttp.MethodPost, "/", strings.NewReader(`{"bundle":{},"mappings":{"s1":"`+escaped+`"}}`))
		if !decodeBundleWrapper(w, r, &target) {
			t.Fatalf("valid Unicode scalar rejected: %s", escaped)
		}
	}
}
