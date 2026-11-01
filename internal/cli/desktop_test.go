package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/desktop"
)

func TestDesktopReopensOneInstanceAndAuthenticatedQuitReleasesOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	out := &startOutput{urls: make(chan string, 1)}
	done := make(chan error, 1)
	finished := false
	defer func() {
		cancel()
		if finished {
			return
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("desktop did not drain")
		}
	}()
	go func() {
		done <- runDesktop(ctx, []string{"--data-dir", dir, "--no-open"}, out, io.Discard, func(string) error { t.Error("no-open launched browser"); return nil })
	}()
	var base string
	select {
	case base = <-out.urls:
	case err := <-done:
		finished = true
		t.Fatalf("desktop startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("desktop not ready")
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/api/v1/desktop/identity")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("identity leaked without nonce: %d", resp.StatusCode)
	}
	record, err := desktop.ReadRecord(dir)
	if err != nil || !desktop.Verify(record) {
		t.Fatalf("record verification: %+v %v", record, err)
	}
	var reopened string
	if err = runDesktop(ctx, []string{"--data-dir", dir}, io.Discard, io.Discard, func(url string) error { reopened = url; return nil }); err != nil {
		t.Fatal(err)
	}
	if reopened != base {
		t.Fatalf("second launch opened %q, want %q", reopened, base)
	}
	session, err := client.Get(base + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(session.Body).Decode(&body)
	session.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", base+"/api/v1/desktop/quit", bytes.NewBufferString("{}"))
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	denied, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatalf("quit without token: %d", denied.StatusCode)
	}
	req, _ = http.NewRequest("POST", base+"/api/v1/desktop/quit", strings.NewReader("{}"))
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Findrail-Token", body.Token)
	accepted, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Body.Close()
	if accepted.StatusCode != 202 {
		t.Fatalf("quit: %d", accepted.StatusCode)
	}
	select {
	case err = <-done:
		finished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quit did not stop desktop")
	}
	if _, err = desktop.ReadRecord(dir); !os.IsNotExist(err) {
		t.Fatalf("record after quit: %v", err)
	}
	lock, err := desktop.Acquire(dir)
	if err != nil {
		t.Fatalf("ownership was not released: %v", err)
	}
	lock.Close()
}
