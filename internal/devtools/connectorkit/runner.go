package connectorkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const outputLimit = 1 << 20

var protected = []string{"go.mod", "go.sum", "internal/evaluation", "examples/evaluation", "docs/search-evaluation.md", "VERSION", "build", "release", ".github/workflows/alpha-release.yml", ".github/workflows/package-rehearsal.yml"}

type runner struct {
	root   string
	env    []string
	base   context.Context
	goPath string
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if remaining > n {
			remaining = n
		}
		_, _ = b.Buffer.Write(p[:remaining])
	}
	if remaining < n {
		b.over = true
	}
	return n, nil
}

func Run(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("invalid repository root %q", root)
	}
	if _, err = os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		return fmt.Errorf("invalid repository root %q: root go.mod missing", root)
	}
	if err := requireGo(exec.LookPath); err != nil {
		return err
	}
	base, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	r := runner{root: abs, env: os.Environ(), base: base}
	return runProtected(abs, r.validate)
}

func requireGo(lookPath func(string) (string, error)) error {
	if _, err := lookPath("go"); err != nil {
		return fmt.Errorf("required tool %q: %w", "go", err)
	}
	return nil
}

func runProtected(root string, validate func() error) (retErr error) {
	before, err := snapshot(root)
	if err != nil {
		return err
	}
	defer func() {
		after, snapErr := snapshot(root)
		if snapErr != nil {
			retErr = errors.Join(retErr, snapErr)
			return
		}
		if !equalSnapshots(before, after) {
			retErr = errors.Join(retErr, errors.New("protected/release paths changed during connector kit validation"))
		}
	}()
	return validate()
}

func (r runner) command(dir string, timeout time.Duration, overrides map[string]string, args ...string) error {
	ctx, cancel := context.WithTimeout(r.base, timeout)
	defer cancel()
	goPath := r.goPath
	if goPath == "" {
		goPath = "go"
	}
	cmd := exec.CommandContext(ctx, goPath, args...)
	cmd.Dir = dir
	cmd.Env = replaceEnv(r.env, overrides)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = outputLimit, outputLimit
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.over || stderr.over {
		if err != nil {
			return fmt.Errorf("go %s: %w (output exceeded %d bytes)", strings.Join(args, " "), err, outputLimit)
		}
		return fmt.Errorf("go %s output exceeded %d bytes", strings.Join(args, " "), outputLimit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("go %s: %w", strings.Join(args, " "), ctx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, detail)
	}
	return nil
}

func replaceEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[key]; !ok {
			out = append(out, entry)
		}
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func (r runner) validate() error {
	example := filepath.Join(r.root, "examples", "connectors", "catalog")
	steps := []struct {
		dir     string
		timeout time.Duration
		env     map[string]string
		args    []string
	}{
		{r.root, 3 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"test", "-count=1", "./pkg/connector/conformance"}},
		{r.root, 3 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"vet", "./pkg/connector/conformance"}},
		{r.root, 4 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"test", "-count=1", "-race", "./pkg/connector/conformance"}},
		{r.root, 3 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"mod", "verify"}},
		{example, 3 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"test", "-count=1", "./..."}},
		{example, 3 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"vet", "./..."}},
		{example, 4 * time.Minute, map[string]string{"GOWORK": "off"}, []string{"test", "-count=1", "-race", "./..."}},
	}
	for _, step := range steps {
		if err := r.command(step.dir, step.timeout, step.env, step.args...); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		dir      string
		packages []string
	}{{r.root, []string{"./pkg/connector/conformance"}}, {example, []string{".", "./cmd/catalog-demo", "./cmd/catalog-scan"}}} {
		if err := r.checkDeps(item.dir, item.packages); err != nil {
			return err
		}
	}
	temp, err := os.MkdirTemp("", "findrail-connector-kit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	consumer := filepath.Join(temp, "consumer")
	if err := os.Mkdir(consumer, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(consumerModule(r.root)), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(consumer, "consumer_test.go"), []byte(externalTest), 0600); err != nil {
		return err
	}
	if err := r.command(consumer, 4*time.Minute, map[string]string{"GOWORK": "off"}, "test", "-count=1", "-race", "./..."); err != nil {
		return err
	}
	for _, goos := range []string{"linux", "windows", "darwin"} {
		suffix := ""
		if goos == "windows" {
			suffix = ".exe"
		}
		for _, bin := range []string{"demo", "scan"} {
			command := "./cmd/catalog-demo"
			if bin == "scan" {
				command = "./cmd/catalog-scan"
			}
			out := filepath.Join(temp, bin+"-"+goos+suffix)
			if err := r.command(example, 3*time.Minute, map[string]string{"GOWORK": "off", "GOOS": goos, "GOARCH": "amd64", "CGO_ENABLED": "0"}, "build", "-o", out, command); err != nil {
				return err
			}
		}
	}
	if err := r.command(example, time.Minute, map[string]string{"GOWORK": "off"}, "run", "./cmd/catalog-demo"); err != nil {
		return err
	}
	if err := r.command(example, time.Minute, map[string]string{"GOWORK": "off"}, "run", "./cmd/catalog-scan", "-h"); err != nil {
		return err
	}
	workspace := filepath.Join(temp, "go.work")
	if err := os.WriteFile(workspace, []byte(workspaceModule(r.root, example)), 0600); err != nil {
		return err
	}
	return r.command(r.root, 4*time.Minute, map[string]string{"GOWORK": workspace}, "test", "-count=1", "-race", "-tags=connector_kit", "./internal/connectorcheck")
}

func consumerModule(root string) string {
	return "module example.com/consumer\n\ngo 1.26.0\n\nrequire github.com/Lephiziel/findrail v0.0.0\n\nreplace github.com/Lephiziel/findrail => " + strconv.Quote(root) + "\n"
}

func workspaceModule(root, example string) string {
	return "go 1.26.0\n\nuse (\n\t" + strconv.Quote(root) + "\n\t" + strconv.Quote(example) + "\n)\n"
}

func (r runner) checkDeps(dir string, packages []string) error {
	args := append([]string{"list", "-deps"}, packages...)
	ctx, cancel := context.WithTimeout(r.base, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = replaceEnv(r.env, map[string]string{"GOWORK": "off"})
	cmd.WaitDelay = 2 * time.Second
	var out, stderr limitedBuffer
	out.limit = outputLimit
	stderr.limit = outputLimit
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go list -deps: %w\n%s", err, stderr.String())
	}
	if out.over || stderr.over {
		return errors.New("go list -deps output exceeded limit")
	}
	for _, line := range strings.Split(out.String(), "\n") {
		for _, forbidden := range []string{"github.com/Lephiziel/findrail/internal/", "modernc.org/sqlite", "modelcontextprotocol", "ledongthuc/pdf"} {
			if strings.Contains(line, forbidden) {
				return fmt.Errorf("forbidden dependency %q in %s", line, dir)
			}
		}
	}
	return nil
}

type fileSnapshot struct {
	path string
	hash [32]byte
}
type pathSnapshot struct {
	exists bool
	files  []fileSnapshot
}
type snapshots map[string]pathSnapshot

func snapshot(root string) (snapshots, error) {
	out := make(snapshots, len(protected))
	for _, rel := range protected {
		path := filepath.Join(root, filepath.FromSlash(rel))
		entries := []fileSnapshot{}
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			out[rel] = pathSnapshot{}
			continue
		}
		if err != nil {
			return nil, err
		}
		state := pathSnapshot{exists: true, files: entries}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := addLinkSnapshot(&entries, rel, path); err != nil {
				return nil, err
			}
		} else if info.Mode().IsRegular() {
			if err := addSnapshot(&entries, rel, path); err != nil {
				return nil, err
			}
		} else if info.IsDir() {
			err = filepath.WalkDir(path, func(name string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.Type()&os.ModeSymlink != 0 {
					p, e := filepath.Rel(root, name)
					if e != nil {
						return e
					}
					return addLinkSnapshot(&entries, filepath.ToSlash(p), name)
				}
				if d.Type().IsRegular() {
					p, e := filepath.Rel(root, name)
					if e != nil {
						return e
					}
					return addSnapshot(&entries, filepath.ToSlash(p), name)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		state.files = entries
		out[rel] = state
	}
	return out, nil
}
func addSnapshot(entries *[]fileSnapshot, rel, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	*entries = append(*entries, fileSnapshot{rel, sha256.Sum256(b)})
	return nil
}
func addLinkSnapshot(entries *[]fileSnapshot, rel, path string) error {
	target, err := os.Readlink(path)
	if err != nil {
		return err
	}
	*entries = append(*entries, fileSnapshot{rel, sha256.Sum256([]byte(target))})
	return nil
}
func equalSnapshots(a, b snapshots) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y, ok := b[k]
		if !ok || x.exists != y.exists || len(x.files) != len(y.files) {
			return false
		}
		for i := range x.files {
			if x.files[i] != y.files[i] {
				return false
			}
		}
	}
	return true
}

const externalTest = `package consumer
import ("context";"testing";"github.com/Lephiziel/findrail/pkg/connector";"github.com/Lephiziel/findrail/pkg/connector/conformance")
type adapter struct{}
func(adapter) Source() connector.Source{return connector.Source{ID:"source",Kind:"test",Name:"fixture"}}
func(adapter) Scan(ctx context.Context,emit func(connector.Document)error)(connector.Report,error){d:=connector.Document{ID:"doc",SourceID:"source",Title:"title",URI:"https://example.test/doc",Path:"doc.txt",Content:"synthetic",Hash:"body-v1",MediaType:"text/plain",SizeBytes:9};if err:=emit(d);err!=nil{return connector.Report{},err};return connector.Report{Seen:1},nil}
func TestExternalConsumer(t *testing.T){if _,_,err:=conformance.Observe(context.Background(),adapter{},conformance.DefaultOptions());err!=nil{t.Fatal(err)};if err:=conformance.CheckCallbackError(context.Background(),adapter{},conformance.DefaultOptions());err!=nil{t.Fatal("consumer callback failure was not propagated")}}
`
