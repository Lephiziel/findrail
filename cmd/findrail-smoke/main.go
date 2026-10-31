// Command findrail-smoke contains development-only black-box acceptance helpers.
// It is not included in application archives.
package main

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
	"path/filepath"
	"strconv"
	"strings"
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

func main() {
	if len(os.Args) != 3 {
		fatal("usage: findrail-smoke search-smoke|diagnostics-smoke APP_BINARY")
	}
	if _, err := os.Stat(os.Args[2]); err != nil {
		fatal("candidate binary: %v", err)
	}
	switch os.Args[1] {
	case "search-smoke":
		fatalIf(runSearchSmoke(os.Args[2]))
	case "diagnostics-smoke":
		fatalIf(runDiagnosticsSmoke(os.Args[2]))
	default:
		fatal("unknown smoke %q", os.Args[1])
	}
}

func fatalIf(err error) {
	if err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "findrail-smoke: "+format+"\n", args...)
	os.Exit(1)
}

func runCandidate(binary string, args ...string) (result, error) {
	return runCandidateTimeout(commandTimeout, binary, args...)
}

func runCandidateTimeout(timeout time.Duration, binary string, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result{stdout: stdout.String(), stderr: stderr.String()}, fmt.Errorf("command deadline exceeded: %v", args)
	}
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return r, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.code = exit.ExitCode()
		return r, fmt.Errorf("command %v exited %d: %s", args, r.code, tail(r.stderr))
	}
	return r, fmt.Errorf("command %v: %w", args, err)
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
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		return nil, fmt.Errorf("command unexpectedly succeeded: %v", args)
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("command deadline exceeded: %v", args)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &value); err != nil {
		return nil, fmt.Errorf("invalid failure JSON from %v: %w", args, err)
	}
	return value, nil
}

type cappedBuffer struct {
	b bytes.Buffer
	n int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := outputLimit - b.n
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
	if err := client.waitReady(10 * time.Second); err != nil {
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
	details, err := runCandidateJSON(binary, append(summaryArgs, "--show-paths")...)
	if err != nil {
		return err
	}
	report, ok := details["report"].(map[string]any)
	if !ok || report["committed"] != true || report["complete"] != true {
		return fmt.Errorf("invalid committed report")
	}
	if number(report["skipped_files"]) < 243 || number(report["pruned_directories"]) < 1 ||
		report["observed_files_known"] != true || report["observed_directories_known"] != true ||
		report["observed_entries_known"] != true {
		return fmt.Errorf("diagnostic inventory counts are incomplete")
	}
	if len(mustJSON(report)) > 65536 || strings.Contains(mustJSON(summary), "picture.png") ||
		!strings.Contains(mustJSON(details), "picture.png") {
		return fmt.Errorf("diagnostic report redaction/size contract failed")
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
	restarted, err := runCandidateJSON(binary, summaryArgs...)
	if err != nil || stringValue(nested(restarted, "report", "report_id")) != durableReportID {
		return fmt.Errorf("failed scan replaced committed report")
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
	if err := client.waitReady(15 * time.Second); err != nil {
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
	if err != nil || stringValue(canceled["status"]) != "canceled" || stringValue(canceled["error_code"]) != "canceled" {
		return fmt.Errorf("canceled job contract failed: %v", err)
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
	if _, err := runCandidateJSON(binary, "export-source", "--data-dir", data, "--source", sid, "--output", archive, "--json"); err != nil {
		return err
	}
	imported, err := runCandidateJSON(binary, "import-source", "--data-dir", data, "--name", "Frozen diagnostics fixture", "--json", archive)
	if err != nil {
		return err
	}
	archiveID := stringValue(imported["source_id"])
	archivedReport, err := runCandidateJSON(binary, "source-report", "--data-dir", data, "--source", archiveID, "--json")
	if err != nil || archivedReport["available"] != false {
		return fmt.Errorf("portable report unexpectedly available")
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
	fmt.Println("Findrail diagnostics smoke passed: committed report, bounded redaction, failure preservation, restart, loopback HTTP report, archive report unavailability and cleanup.")
	return nil
}

type candidateProcess struct{ cmd *exec.Cmd }

func startCandidate(binary string, args ...string) (*candidateProcess, error) {
	cmd := exec.Command(binary, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &candidateProcess{cmd: cmd}, nil
}
func (p *candidateProcess) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _, _ = p.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
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
func (c *httpClient) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := c.get("/healthz"); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server did not become ready")
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
