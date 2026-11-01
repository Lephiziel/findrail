package desktop

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalDirectoryHasOneOwnerAndRecordCannotCauseUnverifiedReuse(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	owner, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err = Acquire(alias); err != ErrOwned {
		t.Fatalf("alias acquired second owner: %v", err)
	}
	if _, err = ReadRecord(dir); err == nil {
		t.Fatal("missing record unexpectedly accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	record, err := NewRecord(owner.DataDir, "http://"+listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Publish(record); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadRecord(alias)
	if err != nil {
		t.Fatal(err)
	}
	if Verify(loaded) {
		t.Fatal("unrelated listener verified as Findrail")
	}
	bad := record
	bad.Nonce = "short"
	if err = owner.Publish(bad); err == nil {
		t.Fatal("invalid nonce published")
	}
}

func TestIdentityRequiresExactPrivateRecord(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"product":"Findrail","data_dir":"/tmp/data","nonce":"secret"}`))
	}))
	defer s.Close()
	if Verify(Record{Version: 1, DataDir: "/tmp/data", URL: s.URL, Nonce: "different"}) {
		t.Fatal("identity accepted a mismatched nonce")
	}
}

func TestClosedOwnerCannotRemoveNewOwnersRecord(t *testing.T) {
	dir := t.TempDir()
	old, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewRecord(old.DataDir, "http://127.0.0.1:7766")
	if err != nil {
		t.Fatal(err)
	}
	if err = old.Publish(first); err != nil {
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	second, err := NewRecord(current.DataDir, "http://127.0.0.1:7767")
	if err != nil {
		t.Fatal(err)
	}
	if err = current.Publish(second); err != nil {
		t.Fatal(err)
	}
	if err = old.RemoveRecord(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(dir)
	if err != nil || got.Nonce != second.Nonce {
		t.Fatalf("new owner's record changed: %+v %v", got, err)
	}
	if err = current.RemoveRecord(); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadRecord(dir); !os.IsNotExist(err) {
		t.Fatalf("owner cleanup: %v", err)
	}
}

func TestOversizedRecordAndUnauthenticatedIdentityAreRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, recordName), []byte(strings.Repeat("x", 4097)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecord(dir); err == nil {
		t.Fatal("accepted oversized record")
	}
	nonce := strings.Repeat("a", 64)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Findrail-Desktop-Nonce") != nonce {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`{"product":"Findrail","data_dir":"test","nonce":"` + nonce + `"}`))
	}))
	defer s.Close()
	if !Verify(Record{Version: 1, DataDir: "test", URL: s.URL, Nonce: nonce}) {
		t.Fatal("verified private identity was rejected")
	}
	if Verify(Record{Version: 1, DataDir: "test", URL: s.URL, Nonce: strings.Repeat("b", 64)}) {
		t.Fatal("accepted incorrect private identity")
	}
}
