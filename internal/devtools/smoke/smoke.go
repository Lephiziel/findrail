// Package smoke contains development-only black-box acceptance helpers.
package smoke

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	commandTimeout = 2 * time.Minute
	outputLimit    = 64 << 10
)

type result struct {
	stdout string
	stderr string
	code   int
}

func Run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: findrail-smoke search-smoke|diagnostics-smoke APP_BINARY")
	}
	if _, err := os.Stat(args[1]); err != nil {
		return fmt.Errorf("candidate binary: %w", err)
	}
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	stopServerWatch := context.AfterFunc(runCtx, stopActiveServers)
	defer stopServerWatch()
	setRunContext(runCtx)
	defer func() {
		stopActiveServers()
		setRunContext(nil)
	}()
	switch args[0] {
	case "search-smoke":
		return runSearchSmoke(args[1])
	case "diagnostics-smoke":
		return runDiagnosticsSmoke(args[1])
	default:
		return fmt.Errorf("unknown smoke %q", args[0])
	}
}

func runCandidate(binary string, args ...string) (result, error) {
	return runCandidateTimeout(commandTimeout, binary, args...)
}

func runCandidateTimeout(timeout time.Duration, binary string, args ...string) (result, error) {
	parent := context.Background()
	if active := getRunContext(); active != nil {
		parent = active
	}
	parent, stopSignals := signal.NotifyContext(parent, os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	var tree processTree
	var treeMu sync.Mutex
	cmd.Cancel = func() error {
		treeMu.Lock()
		defer treeMu.Unlock()
		if tree != nil {
			return tree.stop()
		}
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return stopUnattached(cmd.Process)
	}
	if err := cmd.Start(); err != nil {
		return result{}, fmt.Errorf("start command %q: %w", binary, err)
	}
	treeMu.Lock()
	var attachErr error
	tree, attachErr = attachProcessTree(cmd)
	treeMu.Unlock()
	if attachErr != nil {
		_ = cmd.Cancel()
	}
	cancelFinished := make(chan struct{})
	stopCancelWatch := context.AfterFunc(ctx, func() {
		defer close(cancelFinished)
		_ = cmd.Cancel()
	})
	waitErr := cmd.Wait()
	if !stopCancelWatch() {
		<-cancelFinished
	}
	treeMu.Lock()
	if tree != nil {
		tree.cleanup()
	}
	treeMu.Unlock()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	if attachErr != nil {
		return r, fmt.Errorf("attach command process tree: %w", attachErr)
	}
	if stdout.overflow || stderr.overflow {
		return r, fmt.Errorf("command output exceeded %d bytes", outputLimit)
	}
	if ctx.Err() != nil {
		return r, fmt.Errorf("command deadline exceeded: %v", args)
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			r.code = exit.ExitCode()
			return r, fmt.Errorf("command %v exited %d: %s", args, r.code, tail(r.stderr))
		}
		return r, fmt.Errorf("command %v: %w", args, waitErr)
	}
	return r, nil
}

func runCandidateJSON(binary string, args ...string) (map[string]any, error) {
	r, err := runCandidate(binary, args...)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &value); err != nil {
		return nil, fmt.Errorf("invalid JSON from %v: %w", args, err)
	}
	return value, nil
}

func runCandidateFailure(binary string, args ...string) (map[string]any, error) {
	result, err := runCandidateTimeout(commandTimeout, binary, args...)
	if err == nil {
		return nil, fmt.Errorf("command unexpectedly succeeded: %v", args)
	}
	var value map[string]any
	if result.code == 0 || result.stdout == "" || strings.Contains(err.Error(), "deadline") || strings.Contains(err.Error(), "output exceeded") {
		return nil, err
	}
	if err := json.Unmarshal([]byte(result.stdout), &value); err != nil {
		return nil, fmt.Errorf("invalid failure JSON from %v: %w", args, err)
	}
	return value, nil
}

type cappedBuffer struct {
	b        bytes.Buffer
	n        int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := outputLimit - b.n
	if remaining < len(p) {
		b.overflow = true
	}
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.b.Write(p[:remaining])
		} else {
			_, _ = b.b.Write(p)
		}
	}
	b.n += len(p)
	return len(p), nil
}
func (b *cappedBuffer) String() string { return b.b.String() }

func tail(s string) string {
	if len(s) <= 1024 {
		return s
	}
	return s[len(s)-1024:]
}

func runSearchSmoke(binary string) error {
	base, err := os.MkdirTemp("", "findrail-search-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(base)
	root, data := filepath.Join(base, "notes"), filepath.Join(base, "index")
	if err := os.MkdirAll(filepath.Join(root, "docs", "nested"), 0o755); err != nil {
		return err
	}
	for path, content := range map[string]string{
		"docs/guide.md":        "webhook arrives with retry budget",
		"docs/nested/guide.md": "webhook arrives with retry budget",
		"docs/backoff.md":      "backoff idempotency",
		"docs-old/guide.md":    "webhook arrives retry budget deprecated",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	indexed, err := runCandidateJSON(binary, "index", "--data-dir", data, "--json", root)
	if err != nil {
		return err
	}
	source, err := stringField(indexed, "source", "id")
	if err != nil {
		return err
	}
	args := []string{"search", "--data-dir", data, "--source", source, "--mode", "advanced",
		"--format", "text", "--path-prefix", "docs/", "--title-contains", "guide",
		"--limit", "1", "--json", `"webhook arrives" OR backoff`}
	cli, err := runCandidateJSON(binary, args...)
	if err != nil {
		return err
	}
	if number(cli["total"]) != 2 || len(objects(cli["results"])) != 1 {
		return fmt.Errorf("search total/limit mismatch: %s", mustJSON(cli))
	}
	for _, item := range objects(cli["results"]) {
		if !strings.HasPrefix(stringValue(item["path"]), "docs/") {
			return fmt.Errorf("search path filter failed: %s", mustJSON(item))
		}
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	server, err := startCandidate(binary, "serve", "--data-dir", data, "--no-sync", "--addr", port)
	if err != nil {
		return err
	}
	defer server.stop()
	client := newHTTPClient("http://" + port)
	if err := server.waitReady(client, 10*time.Second); err != nil {
		return err
	}
	query := url.Values{"q": {`"webhook arrives" OR backoff`}, "mode": {"advanced"}, "format": {"text"},
		"source": {source}, "path_prefix": {"docs/"}, "title_contains": {"guide"}, "limit": {"1"}}
	httpResult, err := client.get("/api/v1/search?" + query.Encode())
	if err != nil {
		return err
	}
	if number(httpResult["total"]) != number(cli["total"]) || ids(httpResult["results"]) != ids(cli["results"]) {
		return fmt.Errorf("CLI/HTTP result mismatch")
	}
	if _, err := client.get("/api/v1/search?q=retry&q=backoff"); err == nil || !strings.Contains(err.Error(), "400") {
		return fmt.Errorf("repeated q parameter was accepted")
	}
	fmt.Println("Findrail search smoke passed: Advanced phrase/OR, source/format/path/title filters, total-before-limit, CLI/HTTP agreement, and HTTP duplicate rejection.")
	return nil
}

func runDiagnosticsSmoke(binary string) error {
	base, err := os.MkdirTemp("", "findrail-diagnostics-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(base)
	data, sourceRoot := filepath.Join(base, "index"), filepath.Join(base, "source")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		return err
	}
	write := func(name, content string) error {
		return os.WriteFile(filepath.Join(sourceRoot, name), []byte(content), 0o644)
	}
	if err := write("note.md", "synthetic committed note"); err != nil {
		return err
	}
	if err := write("picture.png", "synthetic unsupported image"); err != nil {
		return err
	}
	if err := write("secret-token.txt", "must never be sampled"); err != nil {
		return err
	}
	if err := write("~$private_key.docx", "synthetic Word lock file"); err != nil {
		return err
	}
	if err := writeDocx(filepath.Join(sourceRoot, "synthetic.docx")); err != nil {
		return err
	}
	for i := 0; i < 240; i++ {
		if err := write(fmt.Sprintf("synthetic-%04d.png", i), "synthetic unsupported image"); err != nil {
			return err
		}
	}
	if err := os.Mkdir(filepath.Join(sourceRoot, ".secret"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, ".secret", "not-enumerated.md"), []byte("hidden"), 0o644); err != nil {
		return err
	}
	indexed, err := runCandidateJSON(binary, "index", "--data-dir", data, "--report", "--json", sourceRoot)
	if err != nil {
		return err
	}
	if indexedReport, ok := indexed["report"].(map[string]any); !ok || indexedReport["committed"] != true {
		return fmt.Errorf("index did not return a committed report")
	} else if _, leaked := indexedReport["examples"]; leaked {
		return fmt.Errorf("index summary unexpectedly included diagnostic examples")
	}
	sid, err := stringField(indexed, "source", "id")
	if err != nil {
		return err
	}
	summaryArgs := []string{"source-report", "--data-dir", data, "--source", sid, "--json"}
	summary, err := runCandidateJSON(binary, summaryArgs...)
	if err != nil || summary["available"] != true {
		return fmt.Errorf("committed report unavailable: %v", err)
	}
	durableReportID := stringValue(nested(summary, "report", "report_id"))
	if durableReportID == "" {
		return fmt.Errorf("CLI summary omitted the committed report ID")
	}
	details, err := runCandidateJSON(binary, append(summaryArgs, "--show-paths")...)
	if err != nil {
		return err
	}
	report, ok := details["report"].(map[string]any)
	if !ok || report["committed"] != true || report["complete"] != true {
		return fmt.Errorf("invalid committed report")
	}
	if stringValue(report["id"]) != durableReportID {
		return fmt.Errorf("CLI summary report ID differs from path-details report ID")
	}
	if number(report["skipped_files"]) < 243 || number(report["pruned_directories"]) < 1 ||
		report["observed_files_known"] != true || report["observed_directories_known"] != true ||
		report["observed_entries_known"] != true {
		return fmt.Errorf("diagnostic inventory counts are incomplete")
	}
	if number(report["indexed_documents"]) != number(report["updated_documents"])+number(report["unchanged_documents"]) ||
		number(report["observed_entries"]) != number(report["observed_files"])+number(report["observed_directories"]) ||
		number(indexed["skipped"]) != number(report["skipped_files"])+number(report["skipped_entries"])+number(report["pruned_directories"]) ||
		number(report["redacted_samples"]) < 3 {
		return fmt.Errorf("diagnostic count/redaction totals are inconsistent")
	}
	reasonCounts := make(map[string]int)
	if reasons, ok := report["reasons"].([]any); ok {
		for _, reason := range reasons {
			item, ok := reason.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid diagnostic reason")
			}
			reasonCounts[stringValue(item["code"])] = number(item["count"])
		}
	}
	if reasonCounts["office_lock_file"] != 1 || reasonCounts["hidden_entry"] != 1 {
		return fmt.Errorf("diagnostic skip reasons are incomplete")
	}
	if len(mustJSON(report)) > 65536 || strings.Contains(mustJSON(summary), "picture.png") ||
		!strings.Contains(mustJSON(details), "picture.png") {
		return fmt.Errorf("diagnostic report redaction/size contract failed")
	}
	if err := validateDiagnosticExamples(report); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "broken.pdf"), []byte("%PDF-invalid"), 0o644); err != nil {
		return err
	}
	failure, err := runCandidateFailure(binary, "index", "--data-dir", data, "--json", sourceRoot)
	if err != nil {
		return err
	}
	if failure["committed"] != false {
		return fmt.Errorf("failed scan was committed")
	}
	if stringValue(nested(failure, "failure", "code")) != "scan_failed" ||
		nested(failure, "result", "attempt", "committed") != false ||
		stringValue(nested(failure, "result", "attempt", "failure_code")) != "scan_failed" {
		return fmt.Errorf("failed scan attempt details are incomplete")
	}
	if strings.Contains(mustJSON(failure), sourceRoot) || strings.Contains(mustJSON(failure), "broken.pdf") {
		return fmt.Errorf("default failed-scan diagnostics disclosed a source path")
	}
	failureDetails, err := runCandidateFailure(binary, "index", "--data-dir", data, "--json", "--show-paths", sourceRoot)
	if err != nil || stringValue(nested(failureDetails, "result", "attempt", "failure_path")) != "broken.pdf" {
		return fmt.Errorf("show-paths failed-scan diagnostic missing failure path: %v", err)
	}
	restarted, err := runCandidateJSON(binary, summaryArgs...)
	if err != nil || stringValue(nested(restarted, "report", "report_id")) != durableReportID {
		return fmt.Errorf("failed scan replaced committed report")
	}
	missingIndex := filepath.Join(base, "must-not-be-created")
	if _, err := runCandidate(binary, "source-report", "--data-dir", missingIndex, "--source", sid, "--json"); err == nil {
		return fmt.Errorf("missing source-report unexpectedly succeeded")
	}
	if _, err := os.Stat(missingIndex); !os.IsNotExist(err) {
		return fmt.Errorf("missing source-report created its data directory")
	}
	if err := os.Remove(filepath.Join(sourceRoot, "broken.pdf")); err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	server, err := startCandidate(binary, "start", "--no-open", "--data-dir", data, "--addr", port)
	if err != nil {
		return err
	}
	defer server.stop()
	client := newHTTPClient("http://" + port)
	if err := server.waitReady(client, 15*time.Second); err != nil {
		return err
	}
	if err := client.waitSyncIdle(15 * time.Second); err != nil {
		return err
	}
	durable, err := runCandidateJSON(binary, summaryArgs...)
	if err != nil {
		return err
	}
	durableReportID = stringValue(nested(durable, "report", "report_id"))
	httpSummary, err := client.get("/api/v1/sources/" + url.PathEscape(sid) + "/report")
	if err != nil {
		return err
	}
	if strings.Contains(mustJSON(httpSummary), "picture.png") {
		return fmt.Errorf("default HTTP diagnostics disclosed an example path")
	}
	httpDetails, err := client.get("/api/v1/sources/" + url.PathEscape(sid) + "/report?include_paths=true")
	if err != nil || !strings.Contains(mustJSON(httpDetails), "picture.png") {
		return fmt.Errorf("HTTP include_paths report omitted example paths: %v", err)
	}
	httpDetailsID := stringValue(nested(httpDetails, "report", "id"))
	if httpDetailsID == "" {
		httpDetailsID = stringValue(nested(httpDetails, "report", "report_id"))
	}
	if httpDetailsID != durableReportID {
		return fmt.Errorf("HTTP include_paths report ID differs from CLI: http=%q cli=%q", httpDetailsID, durableReportID)
	}
	httpReportID := stringValue(nested(httpSummary, "report", "report_id"))
	if httpReportID == "" {
		httpReportID = stringValue(nested(httpSummary, "report", "id"))
	}
	if httpReportID != durableReportID {
		return fmt.Errorf("HTTP report differs from CLI report: http=%q cli=%q", httpReportID, durableReportID)
	}
	tokenResponse, err := client.get("/api/v1/session")
	if err != nil {
		return err
	}
	token := stringValue(tokenResponse["token"])
	archive := filepath.Join(base, "source.findrail.zip")
	cancelRoot := filepath.Join(base, "cancel-inventory")
	if err := os.Mkdir(cancelRoot, 0o755); err != nil {
		return err
	}
	for i := 0; i < 10000; i++ {
		if err := os.WriteFile(filepath.Join(cancelRoot, fmt.Sprintf("skip-%05d.png", i)), nil, 0o644); err != nil {
			return err
		}
	}
	accepted, err := client.post("/api/v1/sources", token, map[string]any{"type": "folder", "path": cancelRoot})
	if err != nil {
		return err
	}
	jobID := stringValue(nested(accepted, "job", "id"))
	if jobID == "" {
		return fmt.Errorf("source job was not accepted")
	}
	if _, err := client.post("/api/v1/jobs/"+url.PathEscape(jobID)+"/cancel", token, nil); err != nil {
		return err
	}
	canceled, err := client.waitJob(jobID, 10*time.Second)
	if err != nil || stringValue(canceled["status"]) != "canceled" || stringValue(canceled["error_code"]) != "canceled" ||
		stringValue(nested(canceled, "progress", "failure_code")) != "canceled" ||
		nested(canceled, "progress", "committed") != false || nested(canceled, "progress", "partial") != true {
		return fmt.Errorf("canceled job contract failed: %v", err)
	}
	registered, err := client.get("/api/v1/sources")
	if err != nil {
		return err
	}
	for _, source := range objects(registered["sources"]) {
		if stringValue(source["root"]) == cancelRoot {
			return fmt.Errorf("canceled source was registered")
		}
	}
	configured, err := client.post("/api/v1/sources/"+url.PathEscape(sid)+"/configure", token, map[string]any{"max_docx_bytes": 0})
	if err != nil {
		return err
	}
	configureID := stringValue(nested(configured, "job", "id"))
	configureJob, err := client.waitJob(configureID, 20*time.Second)
	if err != nil || stringValue(configureJob["status"]) != "succeeded" {
		return fmt.Errorf("configure job failed: %v", err)
	}
	if stringValue(configureJob["type"]) != "configure" || nested(configureJob, "progress", "committed") != true ||
		stringValue(nested(configureJob, "progress", "report_id")) == "" {
		return fmt.Errorf("configure completion diagnostics are incomplete")
	}
	configuredReport, err := runCandidateJSON(binary, summaryArgs...)
	if err != nil {
		return err
	}
	configuredPayload, ok := configuredReport["report"].(map[string]any)
	if !ok {
		return fmt.Errorf("configured source report missing")
	}
	if configuredPayload["report_id"] == nested(configureJob, "progress", "report_id") {
		if configuredPayload["operation"] != "configure" {
			return fmt.Errorf("Configure report operation mismatch")
		}
	} else if configuredPayload["operation"] != "refresh" {
		return fmt.Errorf("Configure report was replaced by an unexpected operation")
	}
	if !hasReason(configuredPayload["reasons"], "format_disabled") {
		return fmt.Errorf("Configure report omitted the disabled-format reason")
	}
	registered, err = client.get("/api/v1/sources")
	if err != nil {
		return err
	}
	configuredSource := findSource(registered["sources"], sid)
	if configuredSource == nil || number(configuredSource["max_docx_bytes"]) != 0 {
		return fmt.Errorf("configured DOCX limit was not published")
	}
	searchAfterConfigure, err := client.get("/api/v1/search?q=syntheticdocxmarker")
	if err != nil || number(searchAfterConfigure["total"]) != 0 {
		return fmt.Errorf("disabled DOCX remained searchable")
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "note.md"), []byte("synthetic committed note watcherfresh"), 0o644); err != nil {
		return err
	}
	if err := client.waitSearch("watcherfresh", 15*time.Second); err != nil {
		return err
	}
	watcherReport, err := runCandidateJSON(binary, summaryArgs...)
	if err != nil {
		return err
	}
	if stringValue(nested(watcherReport, "report", "operation")) != "refresh" ||
		stringValue(nested(watcherReport, "report", "report_id")) == stringValue(configuredPayload["report_id"]) {
		return fmt.Errorf("watcher refresh diagnostics were not published")
	}
	if _, err := runCandidateJSON(binary, "export-source", "--data-dir", data, "--source", sid, "--output", archive, "--json"); err != nil {
		return err
	}
	imported, err := runCandidateJSON(binary, "import-source", "--data-dir", data, "--name", "Frozen diagnostics fixture", "--json", archive)
	if err != nil {
		return err
	}
	archiveID := stringValue(imported["source_id"])
	archivedReport, err := runCandidateJSON(binary, "source-report", "--data-dir", data, "--source", archiveID, "--json")
	if err != nil || archivedReport["available"] != false ||
		stringValue(archivedReport["availability"]) != "frozen_origin_report_not_in_portable_v1" {
		return fmt.Errorf("portable report unexpectedly available")
	}
	registered, err = client.get("/api/v1/sources")
	if err != nil {
		return err
	}
	archiveSource := findSource(registered["sources"], archiveID)
	if archiveSource == nil || stringValue(archiveSource["kind"]) != "archive" {
		return fmt.Errorf("imported source missing from inventory")
	}
	syncState, err := client.get("/api/v1/sync")
	if err != nil {
		return err
	}
	for _, source := range objects(syncState["sources"]) {
		if stringValue(source["source_id"]) == archiveID {
			return fmt.Errorf("frozen archive source entered filesystem sync")
		}
	}
	if token == "" {
		return fmt.Errorf("session token missing")
	}
	frozenHTTP, err := client.get("/api/v1/sources/" + url.PathEscape(archiveID) + "/report")
	if err != nil || frozenHTTP["availability"] != "frozen_origin_report_not_in_portable_v1" {
		return fmt.Errorf("frozen report availability contract failed")
	}
	if _, err := runCandidate(binary, "forget", "--data-dir", data, sid); err != nil {
		return err
	}
	if _, err := runCandidate(binary, "forget", "--data-dir", data, archiveID); err != nil {
		return err
	}
	if _, err := runCandidate(binary, append(summaryArgs, "--show-paths")...); err == nil {
		return fmt.Errorf("source report remained available after Forget")
	}
	fmt.Println("Findrail diagnostics smoke passed: committed report, bounded redaction, failure preservation, restart, loopback HTTP report, archive report unavailability and cleanup.")
	return nil
}

type candidateProcess struct {
	cmd      *exec.Cmd
	tree     processTree
	done     chan struct{}
	waitErr  error
	stopOnce sync.Once
	treeMu   sync.Mutex
}

var activeRun struct {
	sync.Mutex
	ctx     context.Context
	servers map[*candidateProcess]struct{}
}

func setRunContext(ctx context.Context) {
	activeRun.Lock()
	activeRun.ctx = ctx
	activeRun.Unlock()
}
func getRunContext() context.Context {
	activeRun.Lock()
	defer activeRun.Unlock()
	return activeRun.ctx
}
func registerServer(server *candidateProcess) {
	activeRun.Lock()
	if activeRun.servers == nil {
		activeRun.servers = make(map[*candidateProcess]struct{})
	}
	activeRun.servers[server] = struct{}{}
	activeRun.Unlock()
}
func unregisterServer(server *candidateProcess) {
	activeRun.Lock()
	delete(activeRun.servers, server)
	activeRun.Unlock()
}
func stopActiveServers() {
	activeRun.Lock()
	servers := make([]*candidateProcess, 0, len(activeRun.servers))
	for server := range activeRun.servers {
		servers = append(servers, server)
	}
	activeRun.Unlock()
	for _, server := range servers {
		server.stop()
	}
}

func startCandidate(binary string, args ...string) (*candidateProcess, error) {
	cmd := exec.Command(binary, args...)
	discard, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = discard, discard
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = discard.Close()
		return nil, err
	}
	_ = discard.Close()
	tree, err := attachProcessTree(cmd)
	if err != nil {
		_ = stopUnattached(cmd.Process)
		_ = cmd.Wait()
		return nil, err
	}
	server := &candidateProcess{cmd: cmd, tree: tree, done: make(chan struct{})}
	registerServer(server)
	go func() {
		server.waitErr = cmd.Wait()
		server.stopTree()
		unregisterServer(server)
		close(server.done)
	}()
	return server, nil
}

func (p *candidateProcess) stopTree() {
	p.treeMu.Lock()
	defer p.treeMu.Unlock()
	if p.tree != nil {
		p.tree.cleanup()
		p.tree = nil
	}
}

func (p *candidateProcess) terminateTree() {
	p.treeMu.Lock()
	defer p.treeMu.Unlock()
	if p.tree != nil {
		_ = p.tree.stop()
	}
}

func (p *candidateProcess) waitReady(client *httpClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("server exited before readiness: %w", p.waitErr)
		default:
		}
		if _, err := client.get("/healthz"); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server did not become ready before deadline")
}

func (p *candidateProcess) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	p.stopOnce.Do(func() {
		unregisterServer(p)
		p.terminateTree()
		select {
		case <-p.done:
		case <-time.After(4 * time.Second):
			p.terminateTree()
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
			}
		}
		p.stopTree()
	})
}

type httpClient struct {
	base string
	http *http.Client
}

func newHTTPClient(base string) *httpClient {
	return &httpClient{base: base, http: &http.Client{Timeout: 4 * time.Second}}
}
func (c *httpClient) get(path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, outputLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > outputLimit {
		return nil, fmt.Errorf("HTTP response exceeded %d bytes", outputLimit)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, tail(string(body)))
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return value, nil
}
func (c *httpClient) post(path, token string, payload map[string]any) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.base)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Findrail-Token", token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, outputLimit+1))
	if err != nil {
		return nil, err
	}
	if len(responseBody) > outputLimit {
		return nil, fmt.Errorf("HTTP response exceeded %d bytes", outputLimit)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, tail(string(responseBody)))
	}
	if len(responseBody) == 0 {
		return map[string]any{}, nil
	}
	var value map[string]any
	if err := json.Unmarshal(responseBody, &value); err != nil {
		return nil, err
	}
	return value, nil
}
func (c *httpClient) waitSyncIdle(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		value, err := c.get("/api/v1/sync")
		if err == nil {
			sources := objects(value["sources"])
			if len(sources) == 1 && stringValue(sources[0]["state"]) == "idle" {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("source sync did not become idle")
}

func (c *httpClient) waitJob(id string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	var value map[string]any
	var err error
	for time.Now().Before(deadline) {
		value, err = c.get("/api/v1/jobs/" + url.PathEscape(id))
		if err == nil {
			status := stringValue(value["status"])
			if status == "succeeded" || status == "failed" || status == "canceled" {
				return value, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return value, fmt.Errorf("job %s did not finish: %v", id, err)
}

func (c *httpClient) waitSearch(query string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		value, err := c.get("/api/v1/search?q=" + url.QueryEscape(query))
		if err == nil && number(value["total"]) == 1 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("search %q did not refresh", query)
}

func freePort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func writeDocx(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	files := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>syntheticdocxmarker</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			return err
		}
	}
	return archive.Close()
}

func stringField(value map[string]any, path ...string) (string, error) {
	current := value
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return "", fmt.Errorf("missing JSON field %q", key)
		}
		current = next
	}
	v, ok := current[path[len(path)-1]].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("missing JSON field %q", strings.Join(path, "."))
	}
	return v, nil
}
func nested(value map[string]any, path ...string) any {
	var current any = value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}
func stringValue(value any) string {
	v, _ := value.(string)
	return v
}
func number(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func validateDiagnosticExamples(report map[string]any) error {
	examples, ok := report["examples"].([]any)
	if !ok || len(examples) > 200 || number(report["examples_omitted"]) == 0 {
		return fmt.Errorf("diagnostic example bound contract failed")
	}
	for _, example := range examples {
		item, ok := example.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid diagnostic example")
		}
		path := stringValue(item["path"])
		if path == "secret-token.txt" || path == "~$private_key.docx" || strings.Contains(path, ".secret") {
			return fmt.Errorf("sensitive diagnostic example was retained")
		}
	}
	return nil
}

func objects(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}
func findSource(value any, id string) map[string]any {
	for _, item := range objects(value) {
		if stringValue(item["id"]) == id {
			return item
		}
	}
	return nil
}
func hasReason(value any, code string) bool {
	for _, item := range objects(value) {
		if stringValue(item["code"]) == code {
			return true
		}
	}
	return false
}
func ids(value any) string {
	var result []string
	for _, item := range objects(value) {
		result = append(result, stringValue(item["id"]))
	}
	return strings.Join(result, ",")
}
func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// Keep strconv linked in builds where compiler diagnostics otherwise obscure
// the numeric JSON helper while debugging a failed smoke.
var _ = strconv.IntSize
