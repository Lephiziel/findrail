package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lephiziel/findrail/internal/cli"
	"github.com/Lephiziel/findrail/internal/search"
)

func TestCLIDocumentSearchJourney(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("durable search journey"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"index", "--data-dir", data, root}, &out, &stderr, "test"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.Run(context.Background(), []string{"search", "--data-dir", data, "--json", "search journey"}, &out, &stderr, "test"); err != nil {
		t.Fatal(err)
	}
	var response search.Response
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || response.Results[0].Title != "notes.md" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if err := cli.Run(context.Background(), []string{"search", "--addr", "localhost:1", "search"}, &out, &stderr, "test"); err == nil {
		t.Fatal("accepted unrelated command flag")
	}
}
