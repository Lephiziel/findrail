package betapromotion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validRequest() Request {
	return Request{
		Repository:       Repository,
		Event:            "workflow_dispatch",
		Ref:              "refs/heads/main",
		Actor:            "Lephiziel",
		WorkflowSHA:      testSHA,
		CandidateSHA:     testSHA,
		CandidateVersion: Version,
		ReleaseVersion:   Version,
		Scope:            Scope,
		PublishRequested: true,
		CandidateRun:     RunIdentity{ID: "101", Attempt: 2},
		PerformanceRun:   RunIdentity{ID: "202", Attempt: 3},
		HandoffRun:       RunIdentity{ID: "303", Attempt: 4},
		HandoffDigest:    "sha256:" + strings.Repeat("b", 64),
	}
}

type fakeRunner struct {
	request        Request
	failEvidence   bool
	respectContext bool
	calls          [][]string
}

func (r *fakeRunner) Run(ctx context.Context, program string, args []string, dir string, _ map[string]string, _ int) ([]byte, error) {
	r.calls = append(r.calls, append([]string{program}, args...))
	if r.respectContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(args) == 0 {
		return nil, errors.New("empty command")
	}
	switch filepath.Base(args[0]) {
	case "beta_evidence_github.py":
		if !containsString(args, "--require-main-head") {
			return nil, errors.New("main guard missing")
		}
		return nil, r.writePreflight(args)
	case "beta_evidence.py":
		if r.failEvidence {
			return nil, errors.New("synthetic technical proof failure")
		}
		return json.Marshal(map[string]string{
			"status": "passed", "candidate_sha": r.request.CandidateSHA,
			"candidate_version": r.request.CandidateVersion,
		})
	case "release_assets.py":
		return []byte("{}\n"), nil
	default:
		return nil, errors.New("unexpected command")
	}
}

func (r *fakeRunner) writePreflight(args []string) error {
	value := flagValue(args, "--download-dir")
	metadataPath := flagValue(args, "--metadata")
	if value == "" || metadataPath == "" {
		return errors.New("preflight destinations missing")
	}
	assets := filepath.Join(value, "assets")
	bundle := filepath.Join(value, "handoff-bundle")
	handoff := filepath.Join(bundle, "handoff")
	if err := os.MkdirAll(assets, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(handoff, 0o700); err != nil {
		return err
	}
	for _, name := range expectedAssets() {
		if err := os.WriteFile(filepath.Join(assets, name), []byte("accepted:"+name), 0o600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(handoff, "publication-notes.md"), []byte("# Approved beta.1\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(handoff, "evidence-index.json"), []byte(`{"readiness":{"final_release":false,"human_pending":["windows_first_run"]}}`), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bundle, "beta-evidence-reports.zip"), []byte("synthetic report"), 0o600); err != nil {
		return err
	}
	metadata := map[string]any{
		"candidate_run":              map[string]any{"run_id": r.request.CandidateRun.ID, "run_attempt": r.request.CandidateRun.Attempt},
		"performance_run":            map[string]any{"run_id": r.request.PerformanceRun.ID, "run_attempt": r.request.PerformanceRun.Attempt},
		"promotion_handoff_run":      map[string]any{"run_id": r.request.HandoffRun.ID, "run_attempt": r.request.HandoffRun.Attempt},
		"promotion_handoff_artifact": map[string]any{"digest": r.request.HandoffDigest},
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(metadataPath, data, 0o600)
}

type fakeGateway struct {
	request Request

	stateDecision  string
	stateErr       error
	mainSHA        string
	mainErr        error
	moveOnUpload   bool
	moveOnDownload bool
	moveOnMainCall int
	mainCalls      int

	createCalls            int
	uploadCalls            int
	publishCalls           int
	events                 []string
	assets                 map[string][]byte
	assetCalls             int
	release                Release
	tagSHA                 string
	tamperDownload         bool
	partialUpload          bool
	extraAsset             bool
	changeNotes            bool
	changeTag              bool
	changeTagAfterDownload bool
	tagMoved               bool
	changeAssetIDOnFinal   bool
	tagOnDraft             bool
	wrongDraftTarget       bool
	tagNotFoundCalls       int
}

func newFakeGateway(request Request) *fakeGateway {
	return &fakeGateway{request: request, stateDecision: "publish", mainSHA: request.CandidateSHA}
}

func (g *fakeGateway) ReleaseState(_ context.Context, _, _ string) (RemoteState, error) {
	g.events = append(g.events, "release-state")
	if g.stateErr != nil {
		return RemoteState{}, g.stateErr
	}
	if g.release.ID > 0 && !g.release.Draft {
		return RemoteState{Decision: "noop", PublishedSHA: g.request.CandidateSHA}, nil
	}
	if g.stateDecision == "noop" {
		return RemoteState{Decision: "noop", PublishedSHA: strings.Repeat("9", 40)}, nil
	}
	return RemoteState{Decision: g.stateDecision}, nil
}

func (g *fakeGateway) MainSHA(context.Context) (string, error) {
	g.events = append(g.events, "main")
	g.mainCalls++
	if g.mainErr != nil {
		return "", g.mainErr
	}
	if g.moveOnMainCall > 0 && g.mainCalls == g.moveOnMainCall {
		return strings.Repeat("c", 40), nil
	}
	return g.mainSHA, nil
}

func (g *fakeGateway) CreateDraft(_ context.Context, tag, targetSHA, notesFile string) (Release, error) {
	g.events = append(g.events, "create-draft")
	g.createCalls++
	notes, err := os.ReadFile(notesFile)
	if err != nil {
		return Release{}, err
	}
	if g.wrongDraftTarget {
		targetSHA = strings.Repeat("d", 40)
	}
	g.release = Release{ID: 77, TagName: tag, TargetCommitish: targetSHA,
		Name: "Findrail " + tag, Draft: true, Prerelease: true, Body: string(notes)}
	if g.tagOnDraft {
		g.tagSHA = targetSHA
	}
	return g.release, nil
}

func (g *fakeGateway) Upload(_ context.Context, _ string, paths []string) error {
	g.events = append(g.events, "upload")
	g.uploadCalls++
	g.assets = make(map[string][]byte)
	for i, path := range paths {
		if g.partialUpload && i == 2 {
			return errors.New("synthetic partial upload")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		g.assets[filepath.Base(path)] = data
	}
	if g.moveOnUpload {
		g.mainSHA = strings.Repeat("c", 40)
	}
	return nil
}

func (g *fakeGateway) Release(_ context.Context, id int64) (Release, error) {
	g.events = append(g.events, "release")
	if id != g.release.ID {
		return Release{}, errors.New("release ID mismatch")
	}
	current := g.release
	if g.changeNotes {
		current.Body += "changed"
	}
	return current, nil
}

func (g *fakeGateway) Assets(_ context.Context, _ int64) ([]RemoteAsset, error) {
	g.events = append(g.events, "assets")
	g.assetCalls++
	assets := make([]RemoteAsset, 0, len(g.assets)+1)
	for i, name := range expectedAssets() {
		data, ok := g.assets[name]
		if !ok {
			continue
		}
		sum := sha256.Sum256(data)
		id := int64(i + 1)
		if g.changeAssetIDOnFinal && g.assetCalls > 1 {
			id += 100
		}
		assets = append(assets, RemoteAsset{ID: id, Name: name, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	if g.extraAsset {
		assets = append(assets, RemoteAsset{ID: 99, Name: "extra.txt", Size: 1, Digest: "sha256:" + strings.Repeat("0", 64)})
	}
	return assets, nil
}

func (g *fakeGateway) TagSHA(context.Context, string) (string, error) {
	g.events = append(g.events, "tag")
	if g.changeTag || g.tagMoved {
		return strings.Repeat("d", 40), nil
	}
	if g.tagSHA == "" {
		g.tagNotFoundCalls++
		return "", errTagNotFound
	}
	return g.tagSHA, nil
}

func (g *fakeGateway) Download(_ context.Context, assets []RemoteAsset, directory string) error {
	g.events = append(g.events, "download")
	for _, asset := range assets {
		data, ok := g.assets[asset.Name]
		if !ok {
			return errors.New("asset unavailable")
		}
		if g.tamperDownload && len(data) > 0 {
			data = append([]byte(nil), data...)
			data[len(data)-1] ^= 1
		}
		if err := os.WriteFile(filepath.Join(directory, asset.Name), data, 0o600); err != nil {
			return err
		}
	}
	if g.moveOnDownload {
		g.mainSHA = strings.Repeat("c", 40)
	}
	if g.changeTagAfterDownload {
		g.tagMoved = true
	}
	return nil
}

func (g *fakeGateway) Publish(context.Context, string) error {
	g.events = append(g.events, "publish")
	g.publishCalls++
	g.release.Draft = false
	g.tagSHA = g.release.TargetCommitish
	return nil
}

func configForTest(t *testing.T, runner *fakeRunner, gateway *fakeGateway) (Config, Dependencies) {
	t.Helper()
	parent := t.TempDir()
	return Config{RepoRoot: parent, TempParent: parent, AuditFile: filepath.Join(parent, "audit.json")},
		Dependencies{Runner: runner, Gateway: gateway}
}

func runTest(t *testing.T, request Request, mode Mode, runner *fakeRunner, gateway *fakeGateway, ctx context.Context) (Result, error, Config) {
	t.Helper()
	config, deps := configForTest(t, runner, gateway)
	runner.request = request
	result, err := Run(ctx, request, config, mode, deps)
	return result, err, config
}

func TestValidateRequestFailsClosedForNonPromotionContexts(t *testing.T) {
	base := validRequest()
	cases := []struct {
		name   string
		change func(*Request)
	}{
		{"pull request", func(r *Request) { r.Event = "pull_request" }},
		{"push", func(r *Request) { r.Event = "push" }},
		{"fork", func(r *Request) { r.Repository = "fork/findrail" }},
		{"non-main", func(r *Request) { r.Ref = "refs/heads/topic" }},
		{"short candidate", func(r *Request) { r.CandidateSHA = "abc" }},
		{"workflow mismatch", func(r *Request) { r.WorkflowSHA = strings.Repeat("e", 40) }},
		{"unknown version", func(r *Request) { r.CandidateVersion = "0.1.0-beta.2" }},
		{"scope mismatch", func(r *Request) { r.Scope = "stable" }},
		{"unapproved digest", func(r *Request) { r.HandoffDigest = "" }},
		{"uppercase digest", func(r *Request) { r.HandoffDigest = "sha256:" + strings.Repeat("B", 64) }},
		{"bad candidate run", func(r *Request) { r.CandidateRun.ID = "0" }},
		{"bad attempt", func(r *Request) { r.HandoffRun.Attempt = 0 }},
		{"non-owner live write", func(r *Request) { r.Actor = "contributor" }},
		{"publish false", func(r *Request) { r.PublishRequested = false }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.change(&request)
			if err := ValidateRequest(request, PublishMode); err == nil {
				t.Fatal("invalid request was accepted")
			}
			runner := &fakeRunner{request: request}
			gateway := newFakeGateway(request)
			_, err := Run(context.Background(), request, Config{RepoRoot: t.TempDir()}, PublishMode,
				Dependencies{Runner: runner, Gateway: gateway})
			if err == nil {
				t.Fatal("invalid request reached successful promotion")
			}
			if len(runner.calls) != 0 || len(gateway.events) != 0 {
				t.Fatalf("invalid request reached command/API adapter: commands=%v api=%v", runner.calls, gateway.events)
			}
		})
	}
}

func TestValidReadOnlyVerificationNeverWrites(t *testing.T) {
	request := validRequest()
	request.PublishRequested = false
	runner := &fakeRunner{request: request}
	gateway := newFakeGateway(request)
	result, err, config := runTest(t, request, VerifyMode, runner, gateway, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "verified_read_only" || len(result.AssetInventory) != 8 || result.PublicationNotesSHA256 == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if gateway.createCalls+gateway.uploadCalls+gateway.publishCalls != 0 {
		t.Fatalf("read-only verification reached a write: %+v", gateway)
	}
	assertWorkspaceClean(t, config.TempParent)
}

func TestTechnicalProofFailureAndCanceledPreflightNeverWrite(t *testing.T) {
	for _, name := range []string{"technical proof failed", "context canceled"} {
		t.Run(name, func(t *testing.T) {
			request := validRequest()
			runner := &fakeRunner{request: request, failEvidence: name == "technical proof failed", respectContext: true}
			gateway := newFakeGateway(request)
			ctx := context.Background()
			if name == "context canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err, config := runTest(t, request, PublishMode, runner, gateway, ctx)
			if err == nil {
				t.Fatal("failed verification unexpectedly succeeded")
			}
			if gateway.createCalls+gateway.uploadCalls+gateway.publishCalls != 0 {
				t.Fatalf("verification failure reached a write: %+v", gateway)
			}
			assertWorkspaceClean(t, config.TempParent)
		})
	}
}

func TestPublishRechecksAndPublishesExactEightAssets(t *testing.T) {
	request := validRequest()
	runner := &fakeRunner{request: request}
	gateway := newFakeGateway(request)
	result, err, config := runTest(t, request, PublishMode, runner, gateway, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "published" || result.PublishedSHA != request.CandidateSHA {
		t.Fatalf("unexpected publication result: %+v", result)
	}
	if gateway.createCalls != 1 || gateway.uploadCalls != 1 || gateway.publishCalls != 1 {
		t.Fatalf("unexpected write counts: create=%d upload=%d publish=%d", gateway.createCalls, gateway.uploadCalls, gateway.publishCalls)
	}
	if gateway.tagNotFoundCalls != 2 {
		t.Fatalf("draft absence was not distinguished from a published tag: missing reads=%d", gateway.tagNotFoundCalls)
	}
	if len(gateway.assets) != 8 || len(result.AssetInventory) != 8 {
		t.Fatalf("did not promote exact eight assets: remote=%d inventory=%d", len(gateway.assets), len(result.AssetInventory))
	}
	if !hasOrderedEvents(gateway.events, []string{"upload", "assets", "download", "release", "assets", "tag", "main", "publish"}) {
		t.Fatalf("public transition did not follow final checks: %v", gateway.events)
	}
	audit, err := os.ReadFile(config.AuditFile)
	if err != nil {
		t.Fatal(err)
	}
	var record auditRecord
	if json.Unmarshal(audit, &record) != nil || record.Status != "published" || len(record.AssetInventory) != 8 {
		t.Fatalf("publication audit is incomplete: %s", audit)
	}
	assertWorkspaceClean(t, config.TempParent)
}

func TestMainMovementBeforeDraftAndDuringUploadOrFinalVerificationBlocksTransition(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(*fakeGateway)
		wantDraft bool
	}{
		{"before first write", func(g *fakeGateway) { g.mainSHA = strings.Repeat("c", 40) }, false},
		{"during upload", func(g *fakeGateway) { g.moveOnUpload = true }, true},
		{"during final asset verification", func(g *fakeGateway) { g.moveOnDownload = true }, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			runner := &fakeRunner{request: request}
			gateway := newFakeGateway(request)
			test.setup(gateway)
			_, err, config := runTest(t, request, PublishMode, runner, gateway, context.Background())
			if err == nil {
				t.Fatal("main movement unexpectedly published")
			}
			if gateway.publishCalls != 0 || (gateway.createCalls > 0) != test.wantDraft {
				t.Fatalf("unexpected writes: create=%d publish=%d", gateway.createCalls, gateway.publishCalls)
			}
			if test.wantDraft && gateway.release.ID == 0 {
				t.Fatal("draft was not retained for partial-write audit")
			}
			status := "failed"
			if test.wantDraft {
				status = "partial"
			}
			assertAuditStatus(t, config.AuditFile, status, test.wantDraft)
			assertWorkspaceClean(t, config.TempParent)
		})
	}
}

func TestDraftMayExposeOnlyAnExactPinnedTargetOrAnExactExistingTag(t *testing.T) {
	t.Run("provider creates tag with draft", func(t *testing.T) {
		request := validRequest()
		runner := &fakeRunner{request: request}
		gateway := newFakeGateway(request)
		gateway.tagOnDraft = true
		result, err, _ := runTest(t, request, PublishMode, runner, gateway, context.Background())
		if err != nil || result.Decision != "published" || gateway.tagNotFoundCalls != 0 {
			t.Fatalf("exact existing tag was not accepted: result=%+v missing=%d err=%v",
				result, gateway.tagNotFoundCalls, err)
		}
	})

	t.Run("draft target is not the accepted SHA", func(t *testing.T) {
		request := validRequest()
		runner := &fakeRunner{request: request}
		gateway := newFakeGateway(request)
		gateway.wrongDraftTarget = true
		_, err, config := runTest(t, request, PublishMode, runner, gateway, context.Background())
		if err == nil || errorCode(err) != "draft_identity_invalid" ||
			gateway.uploadCalls != 0 || gateway.publishCalls != 0 {
			t.Fatalf("wrong draft target was not blocked before upload: upload=%d publish=%d err=%v",
				gateway.uploadCalls, gateway.publishCalls, err)
		}
		assertAuditStatus(t, config.AuditFile, "partial", true)
	})
}

func TestDraftTamperingNotesPartialStateAndAPIErrorNeverPublish(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeGateway)
	}{
		{"asset bytes changed", func(g *fakeGateway) { g.tamperDownload = true }},
		{"extra asset", func(g *fakeGateway) { g.extraAsset = true }},
		{"notes changed", func(g *fakeGateway) { g.changeNotes = true }},
		{"tag target changed", func(g *fakeGateway) { g.changeTag = true }},
		{"wrong target commitish", func(g *fakeGateway) { g.wrongDraftTarget = true }},
		{"tag target changed during final verification", func(g *fakeGateway) { g.changeTagAfterDownload = true }},
		{"asset IDs changed during final verification", func(g *fakeGateway) { g.changeAssetIDOnFinal = true }},
		{"partial upload", func(g *fakeGateway) { g.partialUpload = true }},
		{"ambiguous draft state", func(g *fakeGateway) { g.stateDecision = "partial" }},
		{"release API error", func(g *fakeGateway) { g.stateErr = errors.New("synthetic API error") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			runner := &fakeRunner{request: request}
			gateway := newFakeGateway(request)
			test.setup(gateway)
			_, err, config := runTest(t, request, PublishMode, runner, gateway, context.Background())
			if err == nil {
				t.Fatal("invalid or ambiguous release state unexpectedly succeeded")
			}
			if gateway.publishCalls != 0 {
				t.Fatalf("invalid draft reached public transition: %v", gateway.events)
			}
			assertWorkspaceClean(t, config.TempParent)
		})
	}
}

func TestCompleteHistoricalReleaseIsNoopAndUnknownVersionNeverQueriesRemote(t *testing.T) {
	request := validRequest()
	runner := &fakeRunner{request: request}
	gateway := newFakeGateway(request)
	gateway.stateDecision = "noop"
	result, err, config := runTest(t, request, PublishMode, runner, gateway, context.Background())
	if err != nil || result.Decision != "noop" || result.PublishedSHA != strings.Repeat("9", 40) {
		t.Fatalf("expected immutable no-op, result=%+v err=%v", result, err)
	}
	if gateway.createCalls+gateway.uploadCalls+gateway.publishCalls != 0 {
		t.Fatal("historical no-op attempted a write")
	}
	assertWorkspaceClean(t, config.TempParent)

	request = validRequest()
	request.CandidateVersion = "0.1.0-beta.2"
	runner = &fakeRunner{request: request}
	gateway = newFakeGateway(request)
	if _, err := Run(context.Background(), request, Config{RepoRoot: t.TempDir()}, PublishMode,
		Dependencies{Runner: runner, Gateway: gateway}); err == nil {
		t.Fatal("unknown version was not rejected")
	}
	if len(runner.calls) != 0 || len(gateway.events) != 0 {
		t.Fatalf("unknown version reached a validator or API: runner=%v gateway=%v", runner.calls, gateway.events)
	}
}

func TestRunMetadataAndRemoteAssetDigestsAreBoundToAcceptedInputs(t *testing.T) {
	request := validRequest()
	metadataPath := filepath.Join(t.TempDir(), "metadata.json")
	data := []byte(`{"candidate_run":{"run_id":"101","run_attempt":9},"performance_run":{"run_id":"202","run_attempt":3},"promotion_handoff_run":{"run_id":"303","run_attempt":4},"promotion_handoff_artifact":{"digest":"sha256:` + strings.Repeat("b", 64) + `"}}`)
	if err := os.WriteFile(metadataPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyMetadata(metadataPath, request); err == nil {
		t.Fatal("mismatched run attempt was accepted")
	}
	items := []Asset{{Name: "expected", Size: 4, SHA256: strings.Repeat("a", 64)}}
	if err := verifyRemoteAssets([]RemoteAsset{{ID: 1, Name: "expected", Size: 4, Digest: "sha256:" + strings.Repeat("b", 64)}}, items); err == nil {
		t.Fatal("remote digest mismatch was accepted")
	}
}

func TestRunIdentityParserRejectsAmbiguousValues(t *testing.T) {
	for _, value := range []string{"", "1", "0/1", "01/1", "1/0", "1/01", "1/2/3", " 1/2", "1/-2"} {
		if _, ok := parseRunIdentity(value); ok {
			t.Errorf("accepted run identity %q", value)
		}
	}
	if got, ok := parseRunIdentity("123456/4"); !ok || got != (RunIdentity{ID: "123456", Attempt: 4}) {
		t.Fatalf("valid run identity rejected: %+v %t", got, ok)
	}
}

func TestExecRunnerEnforcesOutputAndContextBounds(t *testing.T) {
	runner := ExecRunner{}
	helperArgs := []string{"-test.run=^TestPromotionSubprocessHelper$", "--"}
	env := map[string]string{"FINDRAIL_PROMOTION_HELPER": "1"}
	env["FINDRAIL_PROMOTION_HELPER_MODE"] = "output"
	if output, err := runner.Run(context.Background(), os.Args[0], helperArgs, "", env, 32); err == nil {
		t.Fatalf("oversized child output was accepted (len=%d, output=%q)", len(output), output)
	}
	env["FINDRAIL_PROMOTION_HELPER_MODE"] = "split-output"
	if _, err := runner.Run(context.Background(), os.Args[0], helperArgs, "", env, 32); err == nil || errorCode(err) != "command_output_exceeded" {
		t.Fatalf("stdout and stderr did not share one output bound: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "bounded.bin")
	env["FINDRAIL_PROMOTION_HELPER_MODE"] = "output"
	if err := runner.RunToFile(context.Background(), os.Args[0], helperArgs, "", env, destination, 32, 32); err == nil || errorCode(err) != "download_size_exceeded" {
		t.Fatalf("oversized child file output was accepted: %v", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed bounded download left a file: %v", err)
	}
	env["FINDRAIL_PROMOTION_HELPER_MODE"] = "wait"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := runner.Run(ctx, os.Args[0], helperArgs, "", env, 32); err == nil || errorCode(err) != "command_deadline_exceeded" {
		t.Fatalf("child deadline was not enforced: %v", err)
	}
}

func TestPromotionSubprocessHelper(t *testing.T) {
	if os.Getenv("FINDRAIL_PROMOTION_HELPER") != "1" {
		return
	}
	switch os.Getenv("FINDRAIL_PROMOTION_HELPER_MODE") {
	case "output":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("x", 1024)))
	case "split-output":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("o", 20)))
		_, _ = os.Stderr.Write([]byte(strings.Repeat("e", 20)))
	case "gh-api-404-json":
		_, _ = os.Stdout.Write([]byte(`{"message":"Not Found","documentation_url":"https://docs.github.com/rest/git/refs#get-a-reference","status":"404"}`))
		_, _ = os.Stderr.Write([]byte("gh: Not Found (HTTP 404)\n"))
		os.Exit(1)
	case "gh-api-403-json":
		_, _ = os.Stdout.Write([]byte(`{"message":"Resource not accessible by integration","documentation_url":"https://docs.github.com/rest/git/refs#get-a-reference","status":"403"}`))
		_, _ = os.Stderr.Write([]byte("gh: Resource not accessible by integration (HTTP 403)\n"))
		os.Exit(1)
	case "gh-api-500-json":
		_, _ = os.Stdout.Write([]byte(`{"message":"Server Error","documentation_url":"https://docs.github.com/rest/git/refs#get-a-reference","status":"500"}`))
		_, _ = os.Stderr.Write([]byte("gh: Server Error (HTTP 500)\n"))
		os.Exit(1)
	case "gh-api-tag-present":
		_, _ = os.Stdout.Write([]byte(`{"object":{"type":"commit","sha":"` + testSHA + `"}}`))
	case "wait":
		time.Sleep(time.Second)
	}
	os.Exit(0)
}

func assertWorkspaceClean(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "audit.json" {
			t.Errorf("temporary promotion material was not cleaned: %s", entry.Name())
		}
	}
}

func assertAuditStatus(t *testing.T, path, status string, partial bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var audit auditRecord
	if json.Unmarshal(data, &audit) != nil || audit.Status != status || audit.ExternalOperationPartial != partial {
		t.Fatalf("unexpected audit state: %s", data)
	}
}

func flagValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func hasOrderedEvents(have, want []string) bool {
	position := 0
	for _, item := range have {
		if position < len(want) && item == want[position] {
			position++
		}
	}
	return position == len(want)
}
