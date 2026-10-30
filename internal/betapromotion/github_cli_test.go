package betapromotion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCommand struct {
	program     string
	args        []string
	limit       int
	destination string
	maxBytes    int64
}

type recordingRunner struct {
	calls []recordedCommand
	body  string
}

func (r *recordingRunner) Run(_ context.Context, program string, args []string, _ string, _ map[string]string, outputLimit int) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{program: program, args: append([]string(nil), args...), limit: outputLimit})
	if len(args) >= 2 && args[0] == "api" && strings.Contains(args[1], "/releases?per_page=100") {
		return []byte(fmt.Sprintf(`[{"id":77,"tag_name":"v%s","name":"Findrail v%s","draft":true,"prerelease":true,"body":%q}]`, Version, Version, r.body)), nil
	}
	return []byte("ok\n"), nil
}

func (r *recordingRunner) RunToFile(_ context.Context, program string, args []string, _ string, _ map[string]string, destination string, maxBytes int64, outputLimit int) error {
	r.calls = append(r.calls, recordedCommand{
		program: program, args: append([]string(nil), args...), limit: outputLimit,
		destination: destination, maxBytes: maxBytes,
	})
	return os.WriteFile(destination, []byte("asset"), 0o600)
}

func TestGitHubCLIUsesExplicitDraftUploadAndPublishCommands(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("approved notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{body: "approved notes\n"}
	cli := NewGitHubCLI(runner, Config{RepoRoot: t.TempDir()})
	draft, err := cli.CreateDraft(context.Background(), "v"+Version, testSHA, notes)
	if err != nil || !validDraft(draft, "v"+Version, "approved notes\n") {
		t.Fatalf("draft command did not return exact draft identity: %+v, %v", draft, err)
	}
	create := runner.calls[0]
	if create.program != "gh" || create.args[0] != "release" || create.args[1] != "create" ||
		!hasAllArgs(create.args, "--draft", "--prerelease", "--latest=false", "--target", testSHA, "--notes-file", notes) {
		t.Fatalf("draft creation is not explicit and unpublished: %+v", create)
	}
	if containsString(create.args, "--verify-tag") || len(create.args) != 14 {
		t.Fatalf("draft creation unexpectedly requires or uploads an existing tag/assets: %+v", create.args)
	}

	paths := make([]string, 0, 8)
	for i := range 8 {
		paths = append(paths, filepath.Join(t.TempDir(), fmt.Sprintf("accepted-%d", i)))
	}
	if err := cli.Upload(context.Background(), "v"+Version, paths); err != nil {
		t.Fatal(err)
	}
	upload := runner.calls[len(runner.calls)-1]
	if upload.args[0] != "release" || upload.args[1] != "upload" || containsString(upload.args, "--clobber") {
		t.Fatalf("upload may delete/replace assets: %+v", upload.args)
	}
	for _, path := range paths {
		if !containsString(upload.args, path) {
			t.Errorf("accepted asset was not named explicitly: %s", path)
		}
	}
	if len(upload.args) != 3+2+len(paths) {
		t.Fatalf("unexpected upload arguments: %+v", upload.args)
	}

	if err := cli.Publish(context.Background(), "v"+Version); err != nil {
		t.Fatal(err)
	}
	publish := runner.calls[len(runner.calls)-1]
	if publish.args[0] != "release" || publish.args[1] != "edit" ||
		!hasAllArgs(publish.args, "--draft=false", "--prerelease", "--latest=false") {
		t.Fatalf("public transition is not an explicit prerelease: %+v", publish.args)
	}
}

func TestGitHubCLIRejectsAmbiguousReleaseListAndUnexpectedOutput(t *testing.T) {
	runner := &recordingRunner{body: "notes"}
	cli := NewGitHubCLI(runner, Config{RepoRoot: t.TempDir()})
	if _, err := cli.CreateDraft(context.Background(), "v"+Version, testSHA, filepath.Join(t.TempDir(), "notes")); err != nil {
		t.Fatal(err)
	}
	if runner.calls[1].limit != maxCommandOutput {
		t.Fatalf("GitHub API output is not bounded: %+v", runner.calls[1])
	}
	if err := cli.Upload(context.Background(), "v"+Version, []string{"only-one"}); err == nil {
		t.Fatal("incomplete upload was accepted")
	}
}

func TestGitHubCLIDownloadsExactAssetIDsWithPerAssetByteBounds(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "downloads")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{body: "notes"}
	cli := NewGitHubCLI(runner, Config{RepoRoot: root})
	assets := make([]RemoteAsset, 0, 8)
	for i, name := range expectedAssets() {
		assets = append(assets, RemoteAsset{ID: int64(200 + i), Name: name, Size: 5, Digest: "sha256:" + strings.Repeat("a", 64)})
	}
	if err := cli.Download(context.Background(), assets, directory); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 8 {
		t.Fatalf("expected eight exact-ID download calls, got %d", len(runner.calls))
	}
	for i, call := range runner.calls {
		if call.program != "gh" || call.args[0] != "api" || call.args[1] != "-H" ||
			call.args[2] != "Accept: application/octet-stream" ||
			call.args[3] != fmt.Sprintf("repos/%s/releases/assets/%d", Repository, 200+i) ||
			call.destination != filepath.Join(directory, assets[i].Name) || call.maxBytes != 5 {
			t.Errorf("download did not use exact asset identity and byte bound: %+v", call)
		}
	}
}

func hasAllArgs(have []string, want ...string) bool {
	for _, value := range want {
		if !containsString(have, value) {
			return false
		}
	}
	return true
}
