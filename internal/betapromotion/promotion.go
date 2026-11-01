package betapromotion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	Repository = "Lephiziel/findrail"
	Version    = "0.1.0-beta.1"
	Scope      = "limited-desktop-beta"

	maxCommandOutput = 16 << 20
	maxAssetBytes    = 512 << 20
	maxAssetTotal    = 1 << 30
	maxNotesBytes    = 1 << 20
)

var (
	shaPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	runIDPattern   = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	errTagNotFound = errors.New("tag_not_found")
)

type Mode string

const (
	VerifyMode  Mode = "verify"
	PublishMode Mode = "publish"
)

type RunIdentity struct {
	ID      string
	Attempt int
}

type Request struct {
	Repository       string
	Event            string
	Ref              string
	Actor            string
	WorkflowSHA      string
	CandidateSHA     string
	CandidateVersion string
	ReleaseVersion   string
	Scope            string
	PublishRequested bool

	CandidateRun   RunIdentity
	PerformanceRun RunIdentity
	HandoffRun     RunIdentity
	HandoffDigest  string
}

type Config struct {
	RepoRoot   string
	Python     string
	GH         string
	TempParent string
	AuditFile  string
}

type CommandRunner interface {
	Run(ctx context.Context, program string, args []string, dir string, env map[string]string, outputLimit int) ([]byte, error)
}

type FileCommandRunner interface {
	RunToFile(ctx context.Context, program string, args []string, dir string, env map[string]string, destination string, maxBytes int64, outputLimit int) error
}

type RemoteState struct {
	Decision     string `json:"decision"`
	PublishedSHA string `json:"published_sha"`
}

type Release struct {
	ID              int64
	TagName         string
	TargetCommitish string
	Name            string
	Draft           bool
	Prerelease      bool
	Body            string
}

type RemoteAsset struct {
	ID     int64
	Name   string
	Size   int64
	Digest string
}

type Gateway interface {
	ReleaseState(ctx context.Context, version, currentSHA string) (RemoteState, error)
	MainSHA(ctx context.Context) (string, error)
	CreateDraft(ctx context.Context, tag, targetSHA, notesFile string) (Release, error)
	Upload(ctx context.Context, tag string, paths []string) error
	Release(ctx context.Context, id int64) (Release, error)
	Assets(ctx context.Context, id int64) ([]RemoteAsset, error)
	TagSHA(ctx context.Context, tag string) (string, error)
	Download(ctx context.Context, assets []RemoteAsset, directory string) error
	Publish(ctx context.Context, tag string) error
}

type Dependencies struct {
	Runner  CommandRunner
	Gateway Gateway
}

type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Result struct {
	Decision               string          `json:"decision"`
	CandidateSHA           string          `json:"candidate_sha"`
	CandidateVersion       string          `json:"candidate_version"`
	CandidateRun           string          `json:"candidate_run"`
	PerformanceRun         string          `json:"performance_run"`
	HandoffRun             string          `json:"handoff_run"`
	PublishedSHA           string          `json:"published_sha,omitempty"`
	HandoffArtifactDigest  string          `json:"handoff_artifact_digest"`
	PublicationNotesSHA256 string          `json:"publication_notes_sha256"`
	AssetInventory         []Asset         `json:"asset_inventory"`
	Readiness              json.RawMessage `json:"readiness,omitempty"`
}

type failure struct {
	code   string
	stderr string
}

func (e failure) Error() string { return e.code }

func fail(code string) error { return failure{code: code} }

func errorCode(err error) string {
	var f failure
	if errors.As(err, &f) {
		return f.code
	}
	return "promotion_failed"
}

func ValidateRequest(request Request, mode Mode) error {
	if mode != VerifyMode && mode != PublishMode {
		return fail("mode_invalid")
	}
	if request.Repository != Repository || request.Event != "workflow_dispatch" ||
		request.Ref != "refs/heads/main" {
		return fail("dispatch_context_invalid")
	}
	if !shaPattern.MatchString(request.WorkflowSHA) || request.WorkflowSHA != request.CandidateSHA {
		return fail("candidate_sha_invalid")
	}
	if request.CandidateVersion != Version || request.ReleaseVersion != Version {
		return fail("version_contract_invalid")
	}
	if request.Scope != Scope {
		return fail("promotion_scope_invalid")
	}
	if !runIdentityValid(request.CandidateRun) || !runIdentityValid(request.PerformanceRun) ||
		!runIdentityValid(request.HandoffRun) {
		return fail("run_identity_invalid")
	}
	if !digestPattern.MatchString(request.HandoffDigest) {
		return fail("handoff_digest_invalid")
	}
	if mode == PublishMode && (!request.PublishRequested || request.Actor != "Lephiziel") {
		return fail("live_publish_not_authorized")
	}
	if request.PublishRequested && request.Actor != "Lephiziel" {
		return fail("live_publish_not_authorized")
	}
	return nil
}

func runIdentityValid(identity RunIdentity) bool {
	return runIDPattern.MatchString(identity.ID) && identity.Attempt > 0
}

type runMetadata struct {
	CandidateRun struct {
		RunID      string `json:"run_id"`
		RunAttempt int    `json:"run_attempt"`
	} `json:"candidate_run"`
	PerformanceRun struct {
		RunID      string `json:"run_id"`
		RunAttempt int    `json:"run_attempt"`
	} `json:"performance_run"`
	HandoffRun struct {
		RunID      string `json:"run_id"`
		RunAttempt int    `json:"run_attempt"`
	} `json:"promotion_handoff_run"`
	HandoffArtifact struct {
		Digest string `json:"digest"`
	} `json:"promotion_handoff_artifact"`
}

type evidenceResult struct {
	Status           string `json:"status"`
	CandidateSHA     string `json:"candidate_sha"`
	CandidateVersion string `json:"candidate_version"`
}

type evidenceIndex struct {
	Readiness json.RawMessage `json:"readiness"`
}

type auditRecord struct {
	AuditVersion             int     `json:"audit_version"`
	Status                   string  `json:"status"`
	ErrorCode                string  `json:"error_code,omitempty"`
	Repository               string  `json:"repository"`
	CandidateSHA             string  `json:"candidate_sha"`
	CandidateVersion         string  `json:"candidate_version"`
	Scope                    string  `json:"scope"`
	CandidateRun             string  `json:"candidate_run"`
	PerformanceRun           string  `json:"performance_run"`
	HandoffRun               string  `json:"handoff_run"`
	HandoffArtifactDigest    string  `json:"handoff_artifact_digest"`
	PublicationNotesSHA256   string  `json:"publication_notes_sha256,omitempty"`
	AssetInventory           []Asset `json:"asset_inventory,omitempty"`
	ReleaseID                int64   `json:"release_id,omitempty"`
	ExternalOperationPartial bool    `json:"external_operation_may_be_partial,omitempty"`
}

func Run(ctx context.Context, request Request, config Config, mode Mode, deps Dependencies) (result Result, retErr error) {
	if err := ValidateRequest(request, mode); err != nil {
		return Result{}, err
	}
	if deps.Runner == nil {
		deps.Runner = ExecRunner{}
	}
	if config.RepoRoot == "" {
		return Result{}, fail("repository_root_missing")
	}
	if config.Python == "" {
		config.Python = "python3"
	}
	if config.GH == "" {
		config.GH = "gh"
	}
	if config.TempParent == "" {
		config.TempParent = os.TempDir()
	}
	if deps.Gateway == nil {
		deps.Gateway = NewGitHubCLI(deps.Runner, config)
	}

	work, err := os.MkdirTemp(config.TempParent, "findrail-beta-promotion-")
	if err != nil {
		return Result{}, fail("temporary_workspace_failed")
	}
	defer os.RemoveAll(work)

	audit := auditRecord{
		AuditVersion:          1,
		Status:                "verification_pending",
		Repository:            request.Repository,
		CandidateSHA:          request.CandidateSHA,
		CandidateVersion:      request.CandidateVersion,
		Scope:                 request.Scope,
		CandidateRun:          runIdentityText(request.CandidateRun),
		PerformanceRun:        runIdentityText(request.PerformanceRun),
		HandoffRun:            runIdentityText(request.HandoffRun),
		HandoffArtifactDigest: request.HandoffDigest,
	}
	writeAudit := func() error {
		if config.AuditFile == "" {
			return nil
		}
		return writeJSONAtomic(config.AuditFile, audit)
	}
	writeStarted := false
	defer func() {
		if retErr == nil {
			return
		}
		audit.ErrorCode = errorCode(retErr)
		if writeStarted {
			audit.Status = "partial"
			audit.ExternalOperationPartial = true
		} else {
			audit.Status = "failed"
		}
		_ = writeAudit()
	}()

	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}

	downloads := filepath.Join(work, "downloads")
	metadataPath := filepath.Join(work, "run-metadata.json")
	if err := runPreflight(ctx, deps.Runner, config, request, downloads, metadataPath); err != nil {
		return Result{}, err
	}
	bundle := filepath.Join(downloads, "handoff-bundle")
	handoff := filepath.Join(bundle, "handoff")
	assets := filepath.Join(downloads, "assets")
	if err := verifyMetadata(metadataPath, request); err != nil {
		return Result{}, err
	}

	evidenceArgs := []string{
		filepath.Join("scripts", "beta_evidence.py"), "verify",
		"--candidate-sha", request.CandidateSHA,
		"--candidate-version", request.CandidateVersion,
		"--assets-dir", assets,
		"--handoff-dir", handoff,
		"--reports-zip", filepath.Join(bundle, "beta-evidence-reports.zip"),
		"--trusted-run-metadata", metadataPath,
		"--handoff-artifact-dir", bundle,
	}
	output, err := runBounded(ctx, deps.Runner, config.Python, evidenceArgs, config.RepoRoot, nil, 15*time.Minute, maxCommandOutput)
	if err != nil {
		return Result{}, fail("evidence_verification_failed")
	}
	var evidence evidenceResult
	if json.Unmarshal(output, &evidence) != nil || evidence.Status != "passed" ||
		evidence.CandidateSHA != request.CandidateSHA || evidence.CandidateVersion != request.CandidateVersion {
		return Result{}, fail("evidence_result_invalid")
	}
	if err := runReleaseAssets(ctx, deps.Runner, config, assets, request.CandidateSHA); err != nil {
		return Result{}, err
	}

	indexBytes, err := readRegularBounded(filepath.Join(handoff, "evidence-index.json"), 4<<20)
	if err != nil {
		return Result{}, fail("evidence_index_unavailable")
	}
	var index evidenceIndex
	if json.Unmarshal(indexBytes, &index) != nil || len(index.Readiness) == 0 {
		return Result{}, fail("evidence_index_invalid")
	}

	inventory, err := assetInventory(assets)
	if err != nil {
		return Result{}, err
	}
	notesSource := filepath.Join(handoff, "publication-notes.md")
	notesBytes, err := readRegularBounded(notesSource, maxNotesBytes)
	if err != nil || !utf8.Valid(notesBytes) || bytes.IndexByte(notesBytes, 0) >= 0 {
		return Result{}, fail("publication_notes_invalid")
	}
	notesPath := filepath.Join(work, "reviewed-publication-notes.md")
	if err := os.WriteFile(notesPath, notesBytes, 0o600); err != nil {
		return Result{}, fail("temporary_workspace_failed")
	}
	notesDigest := sha256Hex(notesBytes)
	audit.PublicationNotesSHA256 = notesDigest
	audit.AssetInventory = inventory
	result = Result{
		CandidateSHA:           request.CandidateSHA,
		CandidateVersion:       request.CandidateVersion,
		CandidateRun:           runIdentityText(request.CandidateRun),
		PerformanceRun:         runIdentityText(request.PerformanceRun),
		HandoffRun:             runIdentityText(request.HandoffRun),
		HandoffArtifactDigest:  request.HandoffDigest,
		PublicationNotesSHA256: notesDigest,
		AssetInventory:         inventory,
		Readiness:              append(json.RawMessage(nil), index.Readiness...),
	}
	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}

	state, err := deps.Gateway.ReleaseState(ctx, Version, request.CandidateSHA)
	if err != nil {
		return Result{}, fail("release_state_unavailable")
	}
	if state.Decision == "noop" {
		if !shaPattern.MatchString(state.PublishedSHA) {
			return Result{}, fail("published_release_identity_invalid")
		}
		result.Decision = "noop"
		result.PublishedSHA = state.PublishedSHA
		audit.Status = "noop"
		if err := writeAudit(); err != nil {
			return Result{}, fail("audit_write_failed")
		}
		return result, nil
	}
	if state.Decision != "publish" {
		return Result{}, fail("release_state_ambiguous")
	}

	if mode == VerifyMode {
		mainSHA, err := deps.Gateway.MainSHA(ctx)
		if err != nil || mainSHA != request.CandidateSHA {
			return Result{}, fail("main_moved_after_candidate_freeze")
		}
		result.Decision = "verified_read_only"
		audit.Status = "verified_read_only"
		if err := writeAudit(); err != nil {
			return Result{}, fail("audit_write_failed")
		}
		return result, nil
	}
	if !request.PublishRequested || request.Actor != "Lephiziel" {
		return Result{}, fail("live_publish_not_authorized")
	}

	audit.Status = "verification_passed"
	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}
	// Recheck release/tag state, then put the live-main read directly before draft creation.
	state, err = deps.Gateway.ReleaseState(ctx, Version, request.CandidateSHA)
	if err != nil {
		return Result{}, fail("release_state_unavailable")
	}
	if state.Decision == "noop" {
		if !shaPattern.MatchString(state.PublishedSHA) {
			return Result{}, fail("published_release_identity_invalid")
		}
		result.Decision = "noop"
		result.PublishedSHA = state.PublishedSHA
		audit.Status = "noop"
		if err := writeAudit(); err != nil {
			return Result{}, fail("audit_write_failed")
		}
		return result, nil
	}
	if state.Decision != "publish" {
		return Result{}, fail("release_state_ambiguous")
	}
	audit.Status = "write_pending"
	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}
	mainSHA, err := deps.Gateway.MainSHA(ctx)
	if err != nil || mainSHA != request.CandidateSHA {
		return Result{}, fail("main_moved_before_draft")
	}
	writeStarted = true
	draft, err := deps.Gateway.CreateDraft(ctx, "v"+Version, request.CandidateSHA, notesPath)
	if err != nil {
		return Result{}, fail("draft_creation_failed")
	}
	audit.ReleaseID = draft.ID
	if !validDraft(draft, "v"+Version, request.CandidateSHA, string(notesBytes)) {
		return Result{}, fail("draft_identity_invalid")
	}
	if err := verifyDraftTag(ctx, deps.Gateway, draft.TagName, request.CandidateSHA); err != nil {
		return Result{}, fail("draft_tag_target_mismatch")
	}
	audit.Status = "draft_created"
	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}

	assetPaths := make([]string, 0, len(inventory))
	for _, item := range inventory {
		assetPaths = append(assetPaths, filepath.Join(assets, item.Name))
	}
	if err := deps.Gateway.Upload(ctx, draft.TagName, assetPaths); err != nil {
		return Result{}, fail("asset_upload_failed")
	}
	audit.Status = "assets_uploaded"
	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}
	firstAssets, err := deps.Gateway.Assets(ctx, draft.ID)
	if err != nil || verifyRemoteAssets(firstAssets, inventory) != nil {
		return Result{}, fail("draft_asset_inventory_invalid")
	}
	downloaded := filepath.Join(work, "draft-download")
	if err := os.Mkdir(downloaded, 0o700); err != nil {
		return Result{}, fail("temporary_workspace_failed")
	}
	if err := deps.Gateway.Download(ctx, firstAssets, downloaded); err != nil {
		return Result{}, fail("draft_asset_download_failed")
	}
	if err := verifyDownloadedAssets(downloaded, inventory); err != nil {
		return Result{}, err
	}
	if err := runReleaseAssets(ctx, deps.Runner, config, downloaded, request.CandidateSHA); err != nil {
		return Result{}, err
	}

	current, err := deps.Gateway.Release(ctx, draft.ID)
	if err != nil || !validDraft(current, "v"+Version, request.CandidateSHA, string(notesBytes)) || current.ID != draft.ID {
		return Result{}, fail("draft_changed_before_publish")
	}
	finalAssets, err := deps.Gateway.Assets(ctx, draft.ID)
	if err != nil || verifyRemoteAssets(finalAssets, inventory) != nil || !sameRemoteAssets(firstAssets, finalAssets) {
		return Result{}, fail("draft_asset_inventory_changed")
	}
	if err := verifyDraftTag(ctx, deps.Gateway, draft.TagName, request.CandidateSHA); err != nil {
		return Result{}, fail("draft_tag_target_mismatch")
	}
	// This is the last read before the only public transition.
	mainSHA, err = deps.Gateway.MainSHA(ctx)
	if err != nil || mainSHA != request.CandidateSHA {
		return Result{}, fail("main_moved_before_publish")
	}
	if err := deps.Gateway.Publish(ctx, draft.TagName); err != nil {
		return Result{}, fail("public_transition_failed")
	}
	current, err = deps.Gateway.Release(ctx, draft.ID)
	if err != nil || current.ID != draft.ID || !validPublished(current, "v"+Version, request.CandidateSHA, string(notesBytes)) {
		return Result{}, fail("published_release_invalid")
	}
	tagSHA, err := deps.Gateway.TagSHA(ctx, draft.TagName)
	if err != nil || tagSHA != request.CandidateSHA {
		return Result{}, fail("published_tag_target_mismatch")
	}
	state, err = deps.Gateway.ReleaseState(ctx, Version, request.CandidateSHA)
	if err != nil || state.Decision != "noop" || state.PublishedSHA != request.CandidateSHA {
		return Result{}, fail("published_contract_verification_failed")
	}
	result.Decision = "published"
	result.PublishedSHA = request.CandidateSHA
	audit.Status = "published"
	audit.ErrorCode = ""
	if err := writeAudit(); err != nil {
		return Result{}, fail("audit_write_failed")
	}
	return result, nil
}

func runPreflight(ctx context.Context, runner CommandRunner, config Config, request Request, downloadDir, metadataPath string) error {
	args := []string{
		filepath.Join("scripts", "beta_evidence_github.py"), "preflight",
		"--candidate-sha", request.CandidateSHA,
		"--candidate-version", request.CandidateVersion,
		"--candidate-run-id", request.CandidateRun.ID,
		"--candidate-run-attempt", fmt.Sprint(request.CandidateRun.Attempt),
		"--performance-run-id", request.PerformanceRun.ID,
		"--performance-run-attempt", fmt.Sprint(request.PerformanceRun.Attempt),
		"--handoff-run-id", request.HandoffRun.ID,
		"--handoff-run-attempt", fmt.Sprint(request.HandoffRun.Attempt),
		"--expected-handoff-artifact-digest", request.HandoffDigest,
		"--require-main-head",
		"--download-dir", downloadDir,
		"--metadata", metadataPath,
	}
	_, err := runBounded(ctx, runner, config.Python, args, config.RepoRoot, nil, 15*time.Minute, maxCommandOutput)
	if err != nil {
		return fail("github_evidence_preflight_failed")
	}
	return nil
}

func verifyMetadata(path string, request Request) error {
	data, err := readRegularBounded(path, 16<<20)
	if err != nil {
		return fail("run_metadata_unavailable")
	}
	var metadata runMetadata
	if json.Unmarshal(data, &metadata) != nil {
		return fail("run_metadata_invalid")
	}
	if metadata.CandidateRun.RunID != request.CandidateRun.ID ||
		metadata.CandidateRun.RunAttempt != request.CandidateRun.Attempt ||
		metadata.PerformanceRun.RunID != request.PerformanceRun.ID ||
		metadata.PerformanceRun.RunAttempt != request.PerformanceRun.Attempt ||
		metadata.HandoffRun.RunID != request.HandoffRun.ID ||
		metadata.HandoffRun.RunAttempt != request.HandoffRun.Attempt ||
		metadata.HandoffArtifact.Digest != request.HandoffDigest {
		return fail("run_metadata_mismatch")
	}
	return nil
}

func runReleaseAssets(ctx context.Context, runner CommandRunner, config Config, directory, revision string) error {
	args := []string{
		filepath.Join("scripts", "release_assets.py"), "verify",
		"--directory", directory,
		"--version", Version,
		"--revision", revision,
	}
	if _, err := runBounded(ctx, runner, config.Python, args, config.RepoRoot, nil, 10*time.Minute, maxCommandOutput); err != nil {
		return fail("release_asset_verification_failed")
	}
	return nil
}

func expectedAssets() []string {
	targets := []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"}
	names := make([]string, 0, 8)
	for _, target := range targets {
		extension := ".tar.gz"
		if strings.HasPrefix(target, "windows-") {
			extension = ".zip"
		}
		names = append(names, "findrail_"+Version+"_"+target+extension)
	}
	names = append(names, "findrail-readonly-0.1.0.vsix", "release-manifest.json", "SHA256SUMS.txt")
	sort.Strings(names)
	return names
}

func assetInventory(directory string) ([]Asset, error) {
	names := expectedAssets()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(names) {
		return nil, fail("accepted_asset_set_invalid")
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	if !equalStrings(names, actual) {
		return nil, fail("accepted_asset_set_invalid")
	}
	items := make([]Asset, 0, len(names))
	var total int64
	for _, name := range names {
		item, err := hashRegularFile(filepath.Join(directory, name), maxAssetBytes)
		if err != nil || item.Size <= 0 {
			return nil, fail("accepted_asset_invalid")
		}
		total += item.Size
		if total > maxAssetTotal {
			return nil, fail("accepted_asset_set_too_large")
		}
		items = append(items, item)
	}
	return items, nil
}

func verifyDownloadedAssets(directory string, expected []Asset) error {
	actual, err := assetInventory(directory)
	if err != nil {
		return fail("draft_asset_set_invalid")
	}
	if len(actual) != len(expected) {
		return fail("draft_asset_set_invalid")
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return fail("draft_asset_bytes_mismatch")
		}
	}
	return nil
}

func hashRegularFile(path string, max int64) (Asset, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > max {
		return Asset{}, fail("file_invalid")
	}
	f, err := os.Open(path)
	if err != nil {
		return Asset{}, fail("file_invalid")
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, max+1))
	if err != nil || n != info.Size() || n > max {
		return Asset{}, fail("file_read_failed")
	}
	return Asset{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func verifyRemoteAssets(remote []RemoteAsset, expected []Asset) error {
	if len(remote) != len(expected) {
		return fail("draft_asset_count_mismatch")
	}
	byName := make(map[string]RemoteAsset, len(remote))
	for _, asset := range remote {
		if asset.ID <= 0 || asset.Name == "" {
			return fail("draft_asset_metadata_invalid")
		}
		if _, exists := byName[asset.Name]; exists {
			return fail("draft_asset_metadata_duplicate")
		}
		byName[asset.Name] = asset
	}
	if len(byName) != len(expected) {
		return fail("draft_asset_count_mismatch")
	}
	for _, item := range expected {
		asset, ok := byName[item.Name]
		if !ok || asset.Size != item.Size || asset.Digest != "sha256:"+item.SHA256 {
			return fail("draft_asset_metadata_mismatch")
		}
	}
	return nil
}

func sameRemoteAssets(left, right []RemoteAsset) bool {
	if len(left) != len(right) {
		return false
	}
	byName := make(map[string]RemoteAsset, len(left))
	for _, item := range left {
		if _, ok := byName[item.Name]; ok {
			return false
		}
		byName[item.Name] = item
	}
	for _, item := range right {
		if byName[item.Name] != item {
			return false
		}
	}
	return true
}

func verifyDraftTag(ctx context.Context, gateway Gateway, tag, targetSHA string) error {
	tagSHA, err := gateway.TagSHA(ctx, tag)
	if errors.Is(err, errTagNotFound) {
		// An unpublished GitHub draft may have no ref. Its exact target is checked
		// by validDraft before this absence is accepted.
		return nil
	}
	if err != nil || tagSHA != targetSHA {
		return fail("draft_tag_target_mismatch")
	}
	return nil
}

func validDraft(release Release, tag, targetSHA, notes string) bool {
	return release.ID > 0 && release.TagName == tag && release.Name == "Findrail "+tag &&
		release.TargetCommitish == targetSHA && release.Draft && release.Prerelease && release.Body == notes
}

func validPublished(release Release, tag, targetSHA, notes string) bool {
	return release.ID > 0 && release.TagName == tag && release.Name == "Findrail "+tag &&
		release.TargetCommitish == targetSHA && !release.Draft && release.Prerelease && release.Body == notes
}

func readRegularBounded(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > max {
		return nil, errors.New("invalid bounded file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max || int64(len(data)) != info.Size() {
		return nil, errors.New("bounded file read failed")
	}
	return data, nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".beta-promotion-audit-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func runIdentityText(identity RunIdentity) string {
	return identity.ID + "/" + fmt.Sprint(identity.Attempt)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsExpectedAsset(names []string, name string) bool {
	for _, expected := range names {
		if expected == name {
			return true
		}
	}
	return false
}

type boundedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	max      int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.max-b.buffer.Len() {
		b.overflow = true
		return 0, errors.New("command output exceeded bound")
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}

func (b *boundedBuffer) result() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, program string, args []string, dir string, env map[string]string, outputLimit int) ([]byte, error) {
	return runExec(ctx, program, args, dir, env, outputLimit)
}

func (ExecRunner) RunToFile(ctx context.Context, program string, args []string, dir string, env map[string]string, destination string, maxBytes int64, outputLimit int) error {
	return runExecToFile(ctx, program, args, dir, env, destination, maxBytes, outputLimit)
}

type boundedFile struct {
	file     *os.File
	max      int64
	written  int64
	overflow bool
}

func (b *boundedFile) Write(p []byte) (int, error) {
	if int64(len(p)) > b.max-b.written {
		b.overflow = true
		return 0, errors.New("download exceeded byte bound")
	}
	n, err := b.file.Write(p)
	b.written += int64(n)
	return n, err
}

func (b *boundedFile) exceeded() bool { return b.overflow }

func runBounded(parent context.Context, runner CommandRunner, program string, args []string, dir string, env map[string]string, timeout time.Duration, outputLimit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return runner.Run(ctx, program, args, dir, env, outputLimit)
}
