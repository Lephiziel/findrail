// Package resourcecheck implements development-only measurements against an
// explicitly supplied Findrail executable.
package resourcecheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const outputLimit = 1 << 20

type RecoveryOptions struct {
	Binary, ExpectedHash, Revision, WorkDir, Report string
	Schema                                          int
	Deadline                                        float64
}
type recoveryReport struct {
	Case         string `json:"case"`
	Status       string `json:"status"`
	DurationMS   int64  `json:"duration_ms"`
	Phase        string `json:"phase"`
	Revision     string `json:"revision"`
	Schema       int    `json:"schema"`
	Platform     string `json:"platform"`
	BinarySHA256 string `json:"binary_sha256,omitempty"`
	FailureCode  string `json:"failure_code,omitempty"`
}

type child struct {
	cmd            *exec.Cmd
	stdout, stderr bytes.Buffer
	overflow       atomic.Bool
	readers        chan struct{}
}

func start(ctx context.Context, binary string, args ...string) (*child, error) {
	return startWithEnv(ctx, binary, nil, args...)
}

func startWithEnv(ctx context.Context, binary string, env []string, args ...string) (*child, error) {
	c := &child{cmd: exec.CommandContext(ctx, binary, args...), readers: make(chan struct{}, 2)}
	if env != nil {
		c.cmd.Env = append(os.Environ(), env...)
	}
	configureProcessGroup(c.cmd)
	out, outWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errout, errWriter, err := os.Pipe()
	if err != nil {
		out.Close()
		outWriter.Close()
		return nil, err
	}
	c.cmd.Stdout, c.cmd.Stderr = outWriter, errWriter
	if err = c.cmd.Start(); err != nil {
		out.Close()
		outWriter.Close()
		errout.Close()
		errWriter.Close()
		return nil, err
	}
	_ = outWriter.Close()
	_ = errWriter.Close()
	done := make(chan struct{}, 2)
	copyBounded := func(r io.Reader, dst *bytes.Buffer) {
		defer func() { done <- struct{}{} }()
		defer func() {
			if closer, ok := r.(io.Closer); ok {
				_ = closer.Close()
			}
		}()
		b := make([]byte, 8192)
		for {
			n, e := r.Read(b)
			if n > 0 {
				if dst.Len()+n > outputLimit {
					c.overflow.Store(true)
					stopGroup(c.cmd)
					return
				}
				_, _ = dst.Write(b[:n])
			}
			if e != nil {
				return
			}
		}
	}
	go copyBounded(out, &c.stdout)
	go copyBounded(errout, &c.stderr)
	copied := 0
	go func() {
		for copied < 2 {
			<-done
			copied++
		}
		close(c.readers)
	}()
	return c, nil
}

func (c *child) wait(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		// A leader may exit while a descendant still owns its output pipe.
		stopGroup(c.cmd)
		<-c.readers
		if c.overflow.Load() {
			return errors.New("oversized output")
		}
		return err
	case <-ctx.Done():
		stopGroup(c.cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		select {
		case <-c.readers:
		case <-time.After(2 * time.Second):
		}
		return ctx.Err()
	}
}
func stopGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killProcessGroup(cmd)
}

func boundedWait(c *child) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.wait(ctx)
}

func run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	c, err := start(ctx, binary, args...)
	if err != nil {
		return nil, err
	}
	err = c.wait(ctx)
	if err != nil {
		stopGroup(c.cmd)
		return nil, err
	}
	if c.cmd.ProcessState.ExitCode() != 0 {
		return nil, errors.New("command failed")
	}
	return c.stdout.Bytes(), nil
}
func fileHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func executable(path string) (string, string, error) {
	if path == "" {
		return "", "", errors.New("missing binary")
	}
	p, e := filepath.Abs(path)
	if e != nil {
		return "", "", e
	}
	i, e := os.Stat(p)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0111 == 0 {
		return "", "", errors.New("invalid binary")
	}
	h, e := fileHash(p)
	return p, h, e
}
func writeAtomic(path string, v any) error {
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || resolved != dir {
			return errors.New("unsafe report directory")
		}
	}
	path = filepath.Join(dir, filepath.Base(path))
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe report path")
	}
	f, err := os.CreateTemp(dir, ".findrail-resource-report-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	e := json.NewEncoder(f).Encode(v)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}

func RunRecovery(parent context.Context, o RecoveryOptions) int {
	started := time.Now()
	r := recoveryReport{Case: "candidate_refresh_sigkill", Status: "failed", Phase: "unexecuted", Revision: o.Revision, Schema: o.Schema, Platform: runtime.GOOS}
	finish := func(code int) int {
		r.DurationMS = time.Since(started).Milliseconds()
		if err := writeAtomic(o.Report, r); err != nil {
			return 1
		}
		return code
	}
	if o.Report == "" {
		return 1
	}
	if err := writeAtomic(o.Report, r); err != nil {
		return 1
	}
	fail := func(s string) int {
		if parent.Err() != nil {
			r.Status = "interrupted"
			r.FailureCode = "interrupted"
		} else {
			r.FailureCode = s
		}
		return finish(1)
	}
	if runtime.GOOS != "linux" {
		return fail("unsupported_platform")
	}
	if !validRevision(o.Revision) || o.Schema <= 0 {
		return fail("invalid_identity")
	}
	if math.IsNaN(o.Deadline) || math.IsInf(o.Deadline, 0) || o.Deadline <= 0 || o.Deadline > 120 {
		return fail("invalid_deadline")
	}
	bin, digest, err := executable(o.Binary)
	if err != nil {
		return fail("invalid_binary")
	}
	if r.BinarySHA256 != "" && r.BinarySHA256 != digest {
		return fail("binary_changed")
	}
	r.BinarySHA256 = digest
	if o.ExpectedHash != "" && (!validHash(o.ExpectedHash) || o.ExpectedHash != digest) {
		return fail("binary_hash_mismatch")
	}
	if o.WorkDir == "" {
		return fail("invalid_work_dir")
	}
	parentPath, e := filepath.Abs(o.WorkDir)
	if e != nil {
		return fail("invalid_work_dir")
	}
	parentInfo, e := os.Lstat(parentPath)
	if e != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return fail("invalid_work_dir")
	}
	if _, e = os.Stat("/proc/self/stat"); e != nil {
		return fail("proc_unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(o.Deadline*float64(time.Second)))
	defer cancel()
	work, e := os.MkdirTemp(parentPath, "findrail-recovery-")
	if e != nil {
		return fail("setup_failed")
	}
	_ = os.Chmod(work, 0700)
	data, source := filepath.Join(work, "data"), filepath.Join(work, "synthetic-source")
	if e = os.Mkdir(source, 0700); e != nil {
		return fail("setup_failed")
	}
	note, removed := filepath.Join(source, "note.txt"), filepath.Join(source, "removed.txt")
	if e = os.WriteFile(note, []byte("candidateoldtoken\n"+strings.Repeat("safe synthetic filler ", 500000)), 0600); e != nil {
		return fail("setup_failed")
	}
	if e = os.WriteFile(removed, []byte("candidateprunedtoken"), 0600); e != nil {
		return fail("setup_failed")
	}
	if _, e = run(ctx, bin, "index", "--data-dir", data, "--max-bytes", "33554432", source); e != nil {
		return fail("seed_failed")
	}
	for _, q := range []string{"candidateoldtoken", "candidateprunedtoken"} {
		n, e := search(ctx, bin, data, q)
		if e != nil || n != 1 {
			return fail("seed_verification_failed")
		}
	}
	if e = os.WriteFile(note, []byte("candidatenewtoken\n"+strings.Repeat("different synthetic filler ", 500000)), 0600); e != nil {
		return fail("setup_failed")
	}
	if e = os.WriteFile(filepath.Join(source, "added.txt"), []byte("candidateaddedtoken"), 0600); e != nil {
		return fail("setup_failed")
	}
	if e = os.Remove(removed); e != nil {
		return fail("setup_failed")
	}
	before, e := sourceDigest(source)
	if e != nil {
		return fail("setup_failed")
	}
	r.Phase = "candidate_index_started"
	proc, e := start(ctx, bin, "index", "--data-dir", data, "--max-bytes", "33554432", source)
	if e != nil {
		return fail("child_start_failed")
	}
	joined := false
	defer func() {
		if !joined {
			stopGroup(proc.cmd)
			boundedWait(proc)
		}
	}()
	observed := false
	for ctx.Err() == nil {
		if !processRunning(proc.cmd.Process.Pid) {
			stopGroup(proc.cmd)
			return fail("early_exit")
		}
		if openDB(proc.cmd.Process.Pid, filepath.Join(data, "findrail.db")) && cpuTicks(proc.cmd.Process.Pid) > 0 {
			now, hashErr := sourceDigest(source)
			if hashErr == nil && now == before {
				observed = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		stopGroup(proc.cmd)
		return fail("observation_timeout")
	}
	r.Phase = "terminated_commit_unknown"
	stopGroup(proc.cmd)
	boundedWait(proc)
	joined = true
	r.Phase = "reopened"
	doctor, e := run(ctx, bin, "doctor", "--data-dir", data, "--integrity", "--json")
	if e != nil {
		return fail("doctor_failed")
	}
	if !doctorHealthy(doctor, o.Revision, o.Schema) {
		var d map[string]any
		if json.Unmarshal(doctor, &d) != nil {
			return fail("doctor_invalid_json")
		}
		if d["revision"] != o.Revision || number(d["current_write_schema"]) != o.Schema || number(d["index_schema"]) != o.Schema {
			return fail("doctor_identity_mismatch")
		}
		return fail("integrity_failed")
	}
	var d map[string]any
	if json.Unmarshal(doctor, &d) != nil { // doctorHealthy already validated; keep parsing localized to checks below.
		return fail("doctor_identity_mismatch")
	}
	var totals [4]int
	for i, q := range []string{"candidateoldtoken", "candidatenewtoken", "candidateaddedtoken", "candidateprunedtoken"} {
		totals[i], e = search(ctx, bin, data, q)
		if e != nil {
			return fail("search_failed")
		}
	}
	if !completeSnapshot(totals) {
		return fail("mixed_snapshot")
	}
	newBin, newHash, e := executable(bin)
	if e != nil || newBin != bin || newHash != digest {
		return fail("binary_changed")
	}
	r.Status = "passed"
	r.Phase = "verified_old_or_new_commit_unknown"
	return finish(0)
}

func validRevision(s string) bool { return len(s) == 40 && validHex(s) }
func validHash(s string) bool     { return len(s) == 64 && validHex(s) }
func validHex(s string) bool {
	_, e := hex.DecodeString(s)
	return e == nil
}
func number(v any) int {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return -1
	}
	return int(f)
}
func completeSnapshot(t [4]int) bool { return t == [4]int{1, 0, 0, 1} || t == [4]int{0, 1, 1, 0} }
func doctorHealthy(raw []byte, revision string, schema int) bool {
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil || d["revision"] != revision || number(d["current_write_schema"]) != schema || number(d["index_schema"]) != schema {
		return false
	}
	checks, ok := d["checks"].([]any)
	if !ok {
		return false
	}
	good := map[string]bool{}
	for _, item := range checks {
		m, ok := item.(map[string]any)
		if !ok {
			return false
		}
		id, _ := m["id"].(string)
		status, _ := m["status"].(string)
		if id == "" || status == "" {
			return false
		}
		if _, duplicate := good[id]; duplicate {
			return false
		}
		good[id] = status == "ok"
	}
	return good["integrity_quick_check"] && good["integrity_foreign_key_check"]
}
func search(ctx context.Context, bin, data, q string) (int, error) {
	b, e := run(ctx, bin, "search", "--data-dir", data, "--json", q)
	if e != nil {
		return 0, e
	}
	var v struct {
		Total int `json:"total"`
	}
	e = json.Unmarshal(b, &v)
	return v.Total, e
}
func sourceDigest(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, entry := range entries {
		if entry.IsDir() {
			return "", errors.New("unexpected source directory")
		}
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("invalid source fixture")
		}
		fmt.Fprintf(h, "%s\x00%d\x00", entry.Name(), info.Size())
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func processRunning(pid int) bool {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return false
	}
	end := strings.LastIndex(string(b), ")")
	if end < 0 || len(b) <= end+2 {
		return false
	}
	return b[end+2] != 'Z' && b[end+2] != 'X'
}
func openDB(pid int, db string) bool {
	db, _ = filepath.Abs(db)
	fds, e := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if e != nil {
		return false
	}
	for _, fd := range fds {
		p := fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name())
		target, e := os.Readlink(p)
		if e == nil && target == db {
			return true
		}
	}
	return false
}
func cpuTicks(pid int) uint64 {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return 0
	}
	return ticksFromStat(b)
}

func ticksFromStat(b []byte) uint64 {
	s := string(b)
	end := strings.LastIndex(s, ")")
	if end < 0 || len(s) <= end+2 {
		return 0
	}
	f := strings.Fields(s[end+2:])
	if len(f) < 13 {
		return 0
	}
	u, e1 := strconv.ParseUint(f[11], 10, 64)
	v, e2 := strconv.ParseUint(f[12], 10, 64)
	if e1 != nil || e2 != nil {
		return 0
	}
	return u + v
}

type PortableReport struct {
	BinarySHA256      string  `json:"binary_sha256"`
	Platform          string  `json:"platform"`
	Toolchain         string  `json:"toolchain"`
	DocumentTextBytes int     `json:"document_text_bytes"`
	ArchiveBytes      int64   `json:"archive_bytes"`
	ElapsedSeconds    float64 `json:"elapsed_seconds"`
	PeakRSSBytes      *uint64 `json:"peak_rss_bytes"`
	Cancellation      string  `json:"cancellation"`
}

func RunPortable(parent context.Context, path string) (PortableReport, error) {
	var r PortableReport
	if runtime.GOOS != "linux" {
		return r, errors.New("unsupported platform")
	}
	bin, digest, e := executable(path)
	if e != nil {
		return r, e
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	root, e := os.MkdirTemp("", "findrail-portable-resource-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(root)
	source := filepath.Join(root, "near-limit")
	if e = os.Mkdir(source, 0700); e != nil {
		return r, e
	}
	const size = 8 << 20
	pattern := make([]byte, 95)
	for i := range pattern {
		pattern[i] = byte(i + 32)
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = pattern[i%len(pattern)]
	}
	if e = os.WriteFile(filepath.Join(source, "boundary.txt"), payload, 0600); e != nil {
		return r, e
	}
	data := filepath.Join(root, "index")
	if _, e = run(ctx, bin, "index", "--data-dir", data, "--max-bytes", strconv.Itoa(10<<20), "--max-pdf-bytes", "0", "--max-docx-bytes", "0", source); e != nil {
		return r, e
	}
	srcJSON, e := run(ctx, bin, "sources", "--data-dir", data, "--json")
	if e != nil {
		return r, e
	}
	var sources struct {
		Sources []struct {
			ID string `json:"id"`
		} `json:"sources"`
	}
	if json.Unmarshal(srcJSON, &sources) != nil || len(sources.Sources) != 1 {
		return r, errors.New("invalid sources output")
	}
	out := filepath.Join(root, "near-limit.zip")
	startAt := time.Now()
	proc, e := start(ctx, bin, "export-source", "--data-dir", data, "--source", sources.Sources[0].ID, "--output", out, "--json")
	if e != nil {
		return r, e
	}
	var peak *uint64
	for processRunning(proc.cmd.Process.Pid) {
		if v, ok := rss(proc.cmd.Process.Pid); ok && (peak == nil || v > *peak) {
			peak = &v
		}
		select {
		case <-ctx.Done():
			stopGroup(proc.cmd)
			_ = proc.wait(context.Background())
			return r, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	err := proc.wait(ctx)
	if err != nil {
		stopGroup(proc.cmd)
		return r, err
	}
	if proc.cmd.ProcessState.ExitCode() != 0 {
		return r, errors.New("export failed")
	}
	var exported struct {
		ArchiveBytes int64 `json:"archive_bytes"`
	}
	if json.Unmarshal(proc.stdout.Bytes(), &exported) != nil || exported.ArchiveBytes <= 0 {
		return r, errors.New("invalid export JSON")
	}
	elapsed := time.Since(startAt).Seconds()
	_, after, e := executable(bin)
	if e != nil || after != digest {
		return r, errors.New("binary changed")
	}
	r = PortableReport{BinarySHA256: digest, Platform: runtime.GOOS + "/" + runtime.GOARCH, Toolchain: runtime.Version() + "; Go runtime", DocumentTextBytes: size, ArchiveBytes: exported.ArchiveBytes, ElapsedSeconds: elapsed, PeakRSSBytes: peak, Cancellation: "unit-tested via canceled context; CLI interruption removes private staging and publishes no target"}
	return r, nil
}
func rss(pid int) (uint64, bool) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			f := strings.Fields(line)
			if len(f) != 3 || f[2] != "kB" {
				return 0, false
			}
			v, e := strconv.ParseUint(f[1], 10, 64)
			return v * 1024, e == nil
		}
	}
	return 0, false
}
