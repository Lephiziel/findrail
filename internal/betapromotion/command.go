package betapromotion

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

func runExec(ctx context.Context, program string, args []string, directory string, overrides map[string]string, outputLimit int) ([]byte, error) {
	if outputLimit <= 0 {
		outputLimit = maxCommandOutput
	}
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = directory
	cmd.Env = commandEnvironment(overrides)
	output := &boundedBuffer{max: outputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := cmd.Run()
	if output.exceeded() {
		return nil, fail("command_output_exceeded")
	}
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fail("command_deadline_exceeded")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, fail("command_canceled")
		}
		return nil, fail("command_failed")
	}
	return output.result(), nil
}

func runExecToFile(ctx context.Context, program string, args []string, directory string, overrides map[string]string, destination string, maxBytes int64, outputLimit int) error {
	if maxBytes < 0 {
		return fail("download_size_invalid")
	}
	if outputLimit <= 0 {
		outputLimit = 1 << 20
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fail("download_destination_invalid")
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()

	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = directory
	cmd.Env = commandEnvironment(overrides)
	output := &boundedBuffer{max: outputLimit}
	download := &boundedFile{file: file, max: maxBytes}
	cmd.Stdout = download
	cmd.Stderr = output
	runErr := cmd.Run()
	if download.exceeded() {
		return fail("download_size_exceeded")
	}
	if output.exceeded() {
		return fail("command_output_exceeded")
	}
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail("command_deadline_exceeded")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return fail("command_canceled")
		}
		return fail("command_failed")
	}
	if err := file.Sync(); err != nil {
		return fail("download_write_failed")
	}
	if err := file.Close(); err != nil {
		return fail("download_write_failed")
	}
	keep = true
	return nil
}

func commandEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func Main(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != string(VerifyMode) && args[0] != string(PublishMode)) {
		return fail("usage: beta-promote <verify|publish> --candidate-run ID/ATTEMPT --performance-run ID/ATTEMPT --handoff-run ID/ATTEMPT --handoff-artifact-digest sha256:...")
	}
	mode := Mode(args[0])
	flags := newFlagSet("beta-promote " + string(mode))
	candidate := flags.String("candidate-run", "", "exact candidate workflow run ID/attempt")
	performance := flags.String("performance-run", "", "exact performance workflow run ID/attempt")
	handoff := flags.String("handoff-run", "", "exact evidence handoff workflow run ID/attempt")
	digest := flags.String("handoff-artifact-digest", "", "accepted root artifact digest")
	auditPath := flags.String("audit-file", "", "small local publication audit JSON path")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return fail("arguments_invalid")
	}
	candidateRun, ok := parseRunIdentity(*candidate)
	if !ok {
		return fail("candidate_run_invalid")
	}
	performanceRun, ok := parseRunIdentity(*performance)
	if !ok {
		return fail("performance_run_invalid")
	}
	handoffRun, ok := parseRunIdentity(*handoff)
	if !ok {
		return fail("handoff_run_invalid")
	}
	if mode == PublishMode && *auditPath == "" {
		return fail("audit_path_required")
	}
	request, err := requestFromEnvironment(candidateRun, performanceRun, handoffRun, *digest)
	if err != nil {
		return err
	}
	root := os.Getenv("GITHUB_WORKSPACE")
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return fail("repository_root_missing")
		}
	}
	config := Config{
		RepoRoot:   root,
		TempParent: os.Getenv("RUNNER_TEMP"),
		AuditFile:  *auditPath,
	}
	runner := ExecRunner{}
	result, err := Run(ctx, request, config, mode, Dependencies{
		Runner:  runner,
		Gateway: NewGitHubCLI(runner, config),
	})
	if err != nil {
		return fmt.Errorf("beta promotion failed (%s)", errorCode(err))
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fail("result_encoding_failed")
	}
	_, _ = fmt.Fprintln(os.Stdout, string(encoded))
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func parseRunIdentity(value string) (RunIdentity, bool) {
	id, attemptText, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(attemptText, "/") || !runIDPattern.MatchString(id) || attemptText == "" {
		return RunIdentity{}, false
	}
	attempt, err := strconv.Atoi(attemptText)
	if err != nil || attempt <= 0 || strconv.Itoa(attempt) != attemptText {
		return RunIdentity{}, false
	}
	return RunIdentity{ID: id, Attempt: attempt}, true
}

func requestFromEnvironment(candidate, performance, handoff RunIdentity, digest string) (Request, error) {
	publishValue := os.Getenv("PUBLISH")
	if publishValue != "true" && publishValue != "false" {
		return Request{}, fail("publish_input_invalid")
	}
	return Request{
		Repository:       os.Getenv("GITHUB_REPOSITORY"),
		Event:            os.Getenv("GITHUB_EVENT_NAME"),
		Ref:              os.Getenv("GITHUB_REF"),
		Actor:            os.Getenv("GITHUB_ACTOR"),
		WorkflowSHA:      os.Getenv("GITHUB_SHA"),
		CandidateSHA:     os.Getenv("CANDIDATE_SHA"),
		CandidateVersion: os.Getenv("CANDIDATE_VERSION"),
		ReleaseVersion:   os.Getenv("RELEASE_VERSION"),
		Scope:            os.Getenv("PROMOTION_SCOPE"),
		PublishRequested: publishValue == "true",
		CandidateRun:     candidate,
		PerformanceRun:   performance,
		HandoffRun:       handoff,
		HandoffDigest:    digest,
	}, nil
}
