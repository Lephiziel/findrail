package betapromotion

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var invalidAPIPath = regexp.MustCompile(`[^A-Za-z0-9_./?=&-]`)

type GitHubCLI struct {
	runner CommandRunner
	config Config
}

func commandHTTPStatus(err error) int {
	var commandErr failure
	if !errors.As(err, &commandErr) || commandErr.code != "command_failed" {
		return 0
	}
	line := commandErr.stderr
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
	}
	if strings.ContainsAny(line, "\r\n") || !strings.HasPrefix(line, "gh: ") {
		return 0
	}
	marker := " (HTTP "
	index := strings.LastIndex(line, marker)
	if index <= len("gh:") || !strings.HasSuffix(line, ")") {
		return 0
	}
	status, err := strconv.Atoi(line[index+len(marker) : len(line)-1])
	if err != nil || status < 100 || status > 599 {
		return 0
	}
	if status == 404 && line != "gh: Not Found (HTTP 404)" {
		return 0
	}
	return status
}

func NewGitHubCLI(runner CommandRunner, config Config) *GitHubCLI {
	if runner == nil {
		runner = ExecRunner{}
	}
	if config.Python == "" {
		config.Python = "python3"
	}
	if config.GH == "" {
		config.GH = "gh"
	}
	return &GitHubCLI{runner: runner, config: config}
}

func (g *GitHubCLI) ReleaseState(ctx context.Context, version, currentSHA string) (RemoteState, error) {
	args := []string{
		filepath.Join("scripts", "release_gate.py"),
		"--repository", Repository,
		"--version", version,
		"--current-sha", currentSHA,
		"--json",
	}
	output, err := runBounded(ctx, g.runner, g.config.Python, args, g.config.RepoRoot, nil,
		15*time.Minute, 1<<20)
	if err != nil {
		return RemoteState{}, fail("release_state_unavailable")
	}
	var state RemoteState
	if json.Unmarshal(output, &state) != nil || (state.Decision != "publish" && state.Decision != "noop") {
		return RemoteState{}, fail("release_state_invalid")
	}
	if state.Decision == "noop" && !shaPattern.MatchString(state.PublishedSHA) {
		return RemoteState{}, fail("published_release_identity_invalid")
	}
	return state, nil
}

func (g *GitHubCLI) MainSHA(ctx context.Context) (string, error) {
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := g.apiJSON(ctx, "repos/"+Repository+"/git/ref/heads/main", &ref); err != nil {
		return "", err
	}
	if ref.Object.Type != "commit" || !shaPattern.MatchString(ref.Object.SHA) {
		return "", fail("main_ref_invalid")
	}
	return ref.Object.SHA, nil
}

func (g *GitHubCLI) CreateDraft(ctx context.Context, tag, targetSHA, notesFile string) (Release, error) {
	args := []string{
		"release", "create", tag,
		"--repo", Repository,
		"--draft",
		"--prerelease",
		"--latest=false",
		"--target", targetSHA,
		"--title", "Findrail " + tag,
		"--notes-file", notesFile,
	}
	if _, err := g.run(ctx, args, 5*time.Minute, 1<<20); err != nil {
		return Release{}, fail("draft_creation_failed")
	}
	return g.findRelease(ctx, tag)
}

func (g *GitHubCLI) Upload(ctx context.Context, tag string, paths []string) error {
	if len(paths) != 8 {
		return fail("asset_upload_set_invalid")
	}
	args := []string{"release", "upload", tag, "--repo", Repository}
	args = append(args, paths...)
	if _, err := g.run(ctx, args, 15*time.Minute, 1<<20); err != nil {
		return fail("asset_upload_failed")
	}
	return nil
}

func (g *GitHubCLI) Release(ctx context.Context, id int64) (Release, error) {
	var wire releaseWire
	path := "repos/" + Repository + "/releases/" + strconv.FormatInt(id, 10)
	if err := g.apiJSON(ctx, path, &wire); err != nil {
		return Release{}, err
	}
	release := wire.release()
	if release.ID != id {
		return Release{}, fail("release_identity_mismatch")
	}
	return release, nil
}

func (g *GitHubCLI) Assets(ctx context.Context, id int64) ([]RemoteAsset, error) {
	var wire []assetWire
	path := "repos/" + Repository + "/releases/" + strconv.FormatInt(id, 10) + "/assets?per_page=100"
	if err := g.apiJSON(ctx, path, &wire); err != nil {
		return nil, err
	}
	if len(wire) >= 100 {
		return nil, fail("release_asset_list_unbounded")
	}
	assets := make([]RemoteAsset, 0, len(wire))
	for _, item := range wire {
		assets = append(assets, RemoteAsset{ID: item.ID, Name: item.Name, Size: item.Size, Digest: item.Digest})
	}
	return assets, nil
}

func (g *GitHubCLI) TagSHA(ctx context.Context, tag string) (string, error) {
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := g.apiJSON(ctx, "repos/"+Repository+"/git/ref/tags/"+tag, &ref); err != nil {
		return "", err
	}
	if ref.Object.Type == "commit" && shaPattern.MatchString(ref.Object.SHA) {
		return ref.Object.SHA, nil
	}
	if ref.Object.Type != "tag" || !shaPattern.MatchString(ref.Object.SHA) {
		return "", fail("tag_ref_invalid")
	}
	var annotated struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := g.apiJSON(ctx, "repos/"+Repository+"/git/tags/"+ref.Object.SHA, &annotated); err != nil {
		return "", err
	}
	if annotated.Object.Type != "commit" || !shaPattern.MatchString(annotated.Object.SHA) {
		return "", fail("tag_target_invalid")
	}
	return annotated.Object.SHA, nil
}

func (g *GitHubCLI) Download(ctx context.Context, assets []RemoteAsset, directory string) error {
	runner, ok := g.runner.(FileCommandRunner)
	if !ok {
		return fail("bounded_download_unavailable")
	}
	allowed := expectedAssets()
	if len(assets) != len(allowed) {
		return fail("draft_asset_count_mismatch")
	}
	seen := make(map[string]bool, len(assets))
	for _, asset := range assets {
		if asset.ID <= 0 || asset.Size <= 0 || asset.Size > maxAssetBytes || seen[asset.Name] || !containsExpectedAsset(allowed, asset.Name) {
			return fail("draft_asset_metadata_invalid")
		}
		seen[asset.Name] = true
		path := "repos/" + Repository + "/releases/assets/" + strconv.FormatInt(asset.ID, 10)
		args := []string{"api", "-H", "Accept: application/octet-stream", path}
		file := filepath.Join(directory, asset.Name)
		err := runFileBounded(ctx, runner, g.config.GH, args, g.config.RepoRoot, map[string]string{
			"GH_PAGER": "cat", "GH_PROMPT_DISABLED": "1", "NO_COLOR": "1",
		}, file, asset.Size, 15*time.Minute, 1<<20)
		if err != nil {
			return fail("asset_download_failed")
		}
	}
	if len(seen) != len(allowed) {
		return fail("draft_asset_count_mismatch")
	}
	return nil
}

func (g *GitHubCLI) Publish(ctx context.Context, tag string) error {
	args := []string{
		"release", "edit", tag,
		"--repo", Repository,
		"--draft=false",
		"--prerelease",
		"--latest=false",
	}
	if _, err := g.run(ctx, args, 3*time.Minute, 1<<20); err != nil {
		return fail("public_transition_failed")
	}
	return nil
}

func (g *GitHubCLI) findRelease(ctx context.Context, tag string) (Release, error) {
	var wire []releaseWire
	path := "repos/" + Repository + "/releases?per_page=100&page=1"
	if err := g.apiJSON(ctx, path, &wire); err != nil {
		return Release{}, err
	}
	if len(wire) >= 100 {
		return Release{}, fail("release_list_unbounded")
	}
	var found *Release
	for _, item := range wire {
		if item.TagName != tag {
			continue
		}
		release := item.release()
		if found != nil {
			return Release{}, fail("release_tag_ambiguous")
		}
		found = &release
	}
	if found == nil {
		return Release{}, fail("created_draft_unavailable")
	}
	return *found, nil
}

func (g *GitHubCLI) apiJSON(ctx context.Context, path string, target any) error {
	if invalidAPIPath.MatchString(path) {
		return fail("api_path_invalid")
	}
	output, err := g.run(ctx, []string{"api", path}, 2*time.Minute, maxCommandOutput)
	if err != nil {
		if path == "repos/"+Repository+"/git/ref/tags/v"+Version && commandHTTPStatus(err) == 404 {
			return errTagNotFound
		}
		return fail("github_api_failed")
	}
	if json.Unmarshal(output, target) != nil {
		return fail("github_api_response_invalid")
	}
	return nil
}

func (g *GitHubCLI) run(ctx context.Context, args []string, timeout time.Duration, outputLimit int) ([]byte, error) {
	env := map[string]string{
		"GH_PAGER":           "cat",
		"GH_PROMPT_DISABLED": "1",
		"NO_COLOR":           "1",
	}
	return runBounded(ctx, g.runner, g.config.GH, args, g.config.RepoRoot, env, timeout, outputLimit)
}

func runFileBounded(parent context.Context, runner FileCommandRunner, program string, args []string, dir string, env map[string]string, destination string, maxBytes int64, timeout time.Duration, outputLimit int) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return runner.RunToFile(ctx, program, args, dir, env, destination, maxBytes, outputLimit)
}

type releaseWire struct {
	ID              int64  `json:"id"`
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	Name            string `json:"name"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
	Body            string `json:"body"`
}

func (r releaseWire) release() Release {
	return Release{ID: r.ID, TagName: r.TagName, TargetCommitish: r.TargetCommitish,
		Name: r.Name, Draft: r.Draft, Prerelease: r.Prerelease, Body: r.Body}
}

type assetWire struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
