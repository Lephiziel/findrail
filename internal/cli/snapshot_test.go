package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSnapshotHelpWarnsAboutPlaintextProvenance(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"export-source", "--help"}, &out, io.Discard, "test"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("plaintext")) || !bytes.Contains(out.Bytes(), []byte("usernames")) {
		t.Fatalf("missing export privacy help: %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"inspect-export", "--help"}, &out, io.Discard, "test"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not authentication")) || !bytes.Contains(out.Bytes(), []byte("--show-paths")) {
		t.Fatalf("missing inspect privacy help: %s", out.String())
	}
}

func TestNoReplacePublicationIsAtomicAcrossWriters(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "snapshot.zip")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, content := range []string{"complete archive A", "complete archive B"} {
		wg.Add(1)
		go func(body string) {
			defer wg.Done()
			_, err := publishNoReplaceStream(target, func(w io.Writer) error { entered <- struct{}{}; <-release; _, e := io.WriteString(w, body); return e })
			errs <- err
		}(content)
	}
	<-entered
	<-entered
	close(release)
	wg.Wait()
	close(errs)
	success, failed := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			failed++
		}
	}
	if success != 1 || failed != 1 {
		t.Fatalf("publication results: successes=%d failures=%d", success, failed)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete archive A" && string(got) != "complete archive B" {
		t.Fatalf("partial/replaced output: %q", got)
	}
}

func TestFailedPublicationRemovesPrivateStageAndPreservesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "snapshot.zip")
	original := []byte("existing output")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishNoReplaceStream(target, func(w io.Writer) error { _, _ = w.Write([]byte("partial")); return errors.New("injected cancellation") }); err == nil {
		t.Fatal("injected failure succeeded")
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing output changed: %q %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "snapshot.zip" {
		t.Fatalf("staging file leaked: %+v", entries)
	}
}

func TestExportTargetProtectionCanonicalizesSymlinkParentsAndRoots(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("directory symlink creation is platform-policy dependent on Windows")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "registered")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateExportTarget(filepath.Join(alias, "inside.zip"), root); err == nil {
		t.Fatal("symlinked output parent inside source was accepted")
	}
	if err := validateExportTarget(filepath.Join(root, "inside.zip"), alias); err == nil {
		t.Fatal("symlinked protected source root was ignored")
	}
}
