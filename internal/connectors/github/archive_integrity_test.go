package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func archiveMetadata() Metadata {
	return Metadata{RepositoryID: 42, Owner: "Example", Repo: "Demo", RepositoryURL: "https://github.com/Example/Demo", RefMode: "default", MaxBytes: 1024, PolicyVersion: PolicyVersion, SHA: strings.Repeat("a", 40), CommitTime: time.Unix(1, 0)}
}

func archiveWithTail(t *testing.T, extra io.Reader) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)

	for _, header := range []*tar.Header{
		{Name: "repo-root/", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "repo-root/docs/", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "repo-root/docs/readme.md", Typeflag: tar.TypeReg, Mode: 0644, Size: 6},
	} {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size != 0 {
			if _, err := io.WriteString(tw, "needle"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		if _, err := io.Copy(gz, extra); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestArchiveInventoryBoundaries(t *testing.T) {
	t.Run("expanded_budget_includes_trailing_tar_padding", func(t *testing.T) {
		archive := archiveWithTail(t, io.LimitReader(zeroReader{}, MaxExpandedBytes+1))
		_, err := prepareArchive(context.Background(), bytes.NewReader(archive), archiveMetadata())
		if err == nil {
			t.Fatal("accepted more than 128 MiB of expanded data after tar EOF")
		}
	})
	t.Run("second_inventory_in_same_gzip_is_rejected", func(t *testing.T) {
		var second bytes.Buffer
		tw := tar.NewWriter(&second)
		if err := tw.WriteHeader(&tar.Header{Name: "other-root/hidden.md", Mode: 0644, Size: 7}); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(tw, "hidden!")
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := prepareArchive(context.Background(), bytes.NewReader(archiveWithTail(t, &second)), archiveMetadata())
		if err == nil {
			t.Fatal("accepted hidden second tar inventory after the first tar EOF")
		}
	})
	t.Run("selected_path_must_be_directory", func(t *testing.T) {
		meta := archiveMetadata()
		meta.SelectedPath = "docs/readme.md"
		_, err := prepareArchive(context.Background(), bytes.NewReader(archiveWithTail(t, nil)), meta)
		if err == nil {
			t.Fatal("accepted a file as the selected directory")
		}
	})
}

func TestArchiveRejectsDrivePath(t *testing.T) {
	if err := validateTarPath("C:/repo/readme.md"); err == nil {
		t.Error("accepted archive drive path")
	}
}
