package connectorkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReplaceEnvPreservesUnrelatedValues(t *testing.T) {
	got := replaceEnv([]string{"HOME=/home/test", "GOWORK=old", "PATH=/bin"}, map[string]string{"GOWORK": "off", "GOOS": "linux"})
	want := []string{"HOME=/home/test", "PATH=/bin", "GOWORK=off", "GOOS=linux"}
	if len(got) != len(want) || !reflect.DeepEqual(envMap(got), envMap(want)) {
		t.Fatalf("replaceEnv() = %#v, want %#v", got, want)
	}
}

func TestSnapshotIncludesAbsentAndChangedProtectedPaths(t *testing.T) {
	root := t.TempDir()
	before, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if before["go.mod"].exists {
		t.Fatal("missing protected file should have an absent snapshot")
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if equalSnapshots(before, after) {
		t.Fatal("protected file creation was not detected")
	}
}

func TestSnapshotDistinguishesMissingFromEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	before, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "build"), 0700); err != nil {
		t.Fatal(err)
	}
	after, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if equalSnapshots(before, after) {
		t.Fatal("creation of empty protected directory was not detected")
	}
}

func TestSnapshotDetectsNestedMutationAndIgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "build")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "artifact")
	if err := os.WriteFile(file, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if equalSnapshots(before, after) {
		t.Fatal("nested protected mutation was not detected")
	}
}

func TestProtectedMutationReportedOnSuccessAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			root := t.TempDir()
			build := filepath.Join(root, "build")
			if err := os.Mkdir(build, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(build, "keep.bin")
			if err := os.WriteFile(file, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			validationErr := errors.New("injected validation failure")
			err := runProtected(root, func() error {
				if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
					return err
				}
				if fail {
					return validationErr
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "protected/release paths changed") {
				t.Fatalf("protected mutation not reported: %v", err)
			}
			if fail && !errors.Is(err, validationErr) {
				t.Fatalf("validation error not preserved: %v", err)
			}
			data, readErr := os.ReadFile(file)
			if readErr != nil || string(data) != "changed" {
				t.Fatalf("protected content was altered by cleanup: %q %v", data, readErr)
			}
		})
	}
}

func TestInvalidRoot(t *testing.T) {
	if err := Run(t.TempDir()); err == nil || !strings.Contains(err.Error(), "invalid repository root") {
		t.Fatalf("expected invalid-root error, got %v", err)
	}
}

func TestMissingGoIsError(t *testing.T) {
	err := requireGo(func(string) (string, error) { return "", exec.ErrNotFound })
	if err == nil || !strings.Contains(err.Error(), `required tool "go"`) {
		t.Fatalf("missing Go was not an error: %v", err)
	}
}

func TestGeneratedModuleQuotesUnicodeAndSpacePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo with spaces", "資料")
	consumer := t.TempDir()
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(consumerModule(root)), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated go.mod is invalid: %v\n%s", err, output)
	}
	example := filepath.Join(t.TempDir(), "catalog space", "例")
	workspace := workspaceModule(root, example)
	if !strings.Contains(workspace, strconv.Quote(root)) || !strings.Contains(workspace, strconv.Quote(example)) {
		t.Fatalf("workspace paths not quoted: %s", workspace)
	}
}

func TestCommandTimeout(t *testing.T) {
	r := testRunner(t, "timeout")
	err := r.command(r.root, 50*time.Millisecond, nil, "-test.run=TestCommandHelper")
	if err == nil {
		t.Fatal("expected command timeout")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestCommandNonzero(t *testing.T) {
	r := testRunner(t, "failure")
	if err := r.command(r.root, time.Second, nil, "-test.run=TestCommandHelper"); err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("expected preserved nonzero exit, got %v", err)
	}
	var bounded limitedBuffer
	bounded.limit = 4
	if n, err := bounded.Write([]byte("123456")); n != 6 || err != nil || !bounded.over || bounded.String() != "1234" {
		t.Fatalf("bounded write: n=%d err=%v over=%v data=%q", n, err, bounded.over, bounded.String())
	}
}

func TestCommandHelper(t *testing.T) {
	switch os.Getenv("FINDRAIL_CONNECTORKIT_TEST_HELPER") {
	case "timeout":
		time.Sleep(time.Hour)
	case "failure":
		os.Exit(7)
	}
}

func envMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

func testRunner(t *testing.T, mode string) runner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return runner{root: t.TempDir(), env: replaceEnv(os.Environ(), map[string]string{"FINDRAIL_CONNECTORKIT_TEST_HELPER": mode}), base: context.Background(), goPath: exe}
}
