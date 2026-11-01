package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestExplainPathReadOnlyAndBounded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "folder with spaces")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("never read by explanation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "über.md"), []byte("unicode path"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("/notes.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	err := cli.Run(context.Background(), []string{"explain-path", root, "--path", "notes.md", "--path", "missing.md", "--path", "über.md", "--json", "--show-rule"}, &out, &stderr, "test")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version       int  `json:"version"`
		PolicyPresent bool `json:"policy_present"`
		Results       []struct {
			Path, Status, Rule string
			Line               int
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || !got.PolicyPresent || len(got.Results) != 3 || got.Results[0].Status != "excluded_user_rule" || got.Results[0].Rule != "/notes.md" || got.Results[0].Line != 1 || got.Results[1].Status != "not_found" || got.Results[2].Path != "über.md" || got.Results[2].Status != "eligible_by_path_policy" {
		t.Fatalf("explanation: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "index.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("explanation created an index: %v", err)
	}
	indexDir := filepath.Join(root, "private-index")
	out.Reset()
	if err := cli.Run(context.Background(), []string{"explain-path", root, "--path", "private-index/cache.md", "--data-dir", indexDir, "--json"}, &out, &stderr, "test"); err != nil {
		t.Fatal(err)
	}
	var indexDecision struct {
		Results []struct {
			Status   string `json:"status"`
			Reason   string `json:"reason"`
			Ancestor string `json:"ancestor"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &indexDecision); err != nil {
		t.Fatal(err)
	}
	if len(indexDecision.Results) != 1 || indexDecision.Results[0].Status != "excluded_builtin" || indexDecision.Results[0].Reason != "index_directory" || indexDecision.Results[0].Ancestor != "private-index" {
		t.Fatalf("index directory preview: %+v", indexDecision)
	}
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("[malformed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = cli.Run(context.Background(), []string{"explain-path", root, "--path", "notes.md", "--json"}, &out, &stderr, "test")
	if err == nil {
		t.Fatal("malformed policy explanation returned success")
	}
	var failure struct {
		PolicyPresent bool `json:"policy_present"`
		Results       []struct {
			Status    string `json:"status"`
			ErrorCode string `json:"error_code"`
			Line      int    `json:"line"`
		} `json:"results"`
	}
	if e := json.Unmarshal(out.Bytes(), &failure); e != nil {
		t.Fatal(e)
	}
	if !failure.PolicyPresent || len(failure.Results) != 1 || failure.Results[0].Status != "policy_error" || failure.Results[0].ErrorCode != "unsupported_class" || failure.Results[0].Line != 1 {
		t.Fatalf("safe policy error output: %+v", failure)
	}
	for _, args := range [][]string{{"explain-path", root, "--path", "../bad"}, {"explain-path", root, "--path", `C:\notes\a.md`}, {"explain-path", root, "--path", "notes.md", "extra"}, {"explain-path", root, "--wat"}, {"explain-path", root, "--path"}} {
		out.Reset()
		if err := cli.Run(context.Background(), args, &out, &stderr, "test"); err == nil {
			t.Fatalf("accepted invalid args %q", args)
		}
	}
	tooMany := []string{"explain-path", root}
	for i := 0; i < 101; i++ {
		tooMany = append(tooMany, "--path", "missing.md")
	}
	if err := cli.Run(context.Background(), tooMany, &out, &stderr, "test"); err == nil {
		t.Fatal("accepted more than 100 paths")
	}
	// A valid but oversized request fails without returning a partial response.
	long := strings.Repeat(strings.Repeat("x", 250)+"/", 11) + strings.Repeat("x", 250)
	paths := []string{"explain-path", root, "--json"}
	for i := 0; i < 100; i++ {
		paths = append(paths, "--path", fmt.Sprintf("%s%d", long, i))
	}
	out.Reset()
	if err := cli.Run(context.Background(), paths, &out, &stderr, "test"); err == nil || out.Len() != 0 {
		t.Fatalf("output cap: err=%v bytes=%d", err, out.Len())
	}
}
