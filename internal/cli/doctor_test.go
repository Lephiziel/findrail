package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/cli"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func TestDoctorMissingIsReadOnlyAndJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	var out, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"doctor", "--data-dir", dir, "--json"}, &out, &stderr, "test-version"); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("not one JSON report: %q: %v", out.String(), err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("doctor created data directory: %v", err)
	}
	checks := report["checks"].([]any)
	if checks[0].(map[string]any)["code"] != "not_initialized" {
		t.Fatalf("checks: %#v", checks)
	}
	if _, ok := report["data_directory"]; ok {
		t.Fatal("default report disclosed path")
	}
	var human, humanErr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"doctor", "--data-dir", dir, "--show-paths"}, &human, &humanErr, "test-version"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "revision") || !strings.Contains(human.String(), "not_initialized") || !strings.Contains(human.String(), dir) {
		t.Fatalf("human report diverged or omitted explicit path: %s", human.String())
	}
}

func TestDoctorCanceledContextReportsJSONWithoutInspecting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uninspected")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, stderr bytes.Buffer
	err := cli.Run(ctx, []string{"doctor", "--data-dir", dir, "--json"}, &out, &stderr, "test")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Fatalf("canceled doctor exit: %v", err)
	}
	var report struct {
		Checks []struct {
			Code string `json:"code"`
		} `json:"checks"`
	}
	if e := json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatalf("invalid diagnostic JSON %q: %v", out.String(), e)
	}
	found := false
	for _, check := range report.Checks {
		if check.Code == "inspection_canceled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("canceled check missing: %+v", report.Checks)
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatalf("canceled doctor touched index path: %v", e)
	}
}

func TestDoctorSupportedSchemaAndCorruptDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt bool
		code    string
	}{{"current", false, "schema_supported"}, {"corrupt", true, "index_unreadable"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.corrupt {
				if err := os.WriteFile(filepath.Join(dir, "findrail.db"), []byte("not sqlite content"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				s, err := sqlite.Open(context.Background(), dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var out, stderr bytes.Buffer
			err := cli.Run(context.Background(), []string{"doctor", "--data-dir", dir, "--json", "--show-paths"}, &out, &stderr, "test")
			if tc.corrupt && err == nil {
				t.Fatal("corrupt index was reported usable")
			}
			if !tc.corrupt && err != nil {
				t.Fatal(err)
			}
			var report struct {
				Checks []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"checks"`
			}
			if e := json.Unmarshal(out.Bytes(), &report); e != nil {
				t.Fatalf("JSON %q: %v", out.String(), e)
			}
			found := false
			for _, c := range report.Checks {
				if c.Code == tc.code {
					found = true
					if tc.corrupt && c.Message == "not sqlite content" {
						t.Fatal("database error leaked")
					}
				}
			}
			if !found {
				t.Fatalf("missing %s in %+v", tc.code, report.Checks)
			}
		})
	}
}

func TestDoctorOldAndNewerSchemaDoesNotMigrate(t *testing.T) {
	for _, tc := range []struct {
		schema int
		want   string
		exit   int
	}{{3, "schema_upgrade_needed", 0}, {5, "schema_unsupported", 2}} {
		t.Run(tc.want, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "findrail.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE marker(value TEXT); PRAGMA user_version=" + strconv.Itoa(tc.schema)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			err = cli.Run(context.Background(), []string{"doctor", "--data-dir", dir, "--json"}, &out, &stderr, "test")
			if tc.exit == 0 && err != nil {
				t.Fatal(err)
			}
			if tc.exit != 0 {
				var coded interface{ ExitCode() int }
				if !errors.As(err, &coded) || coded.ExitCode() != tc.exit {
					t.Fatalf("exit mapping: %v", err)
				}
			}
			var report struct {
				Checks []struct{ Code, Status string } `json:"checks"`
			}
			if e := json.Unmarshal(out.Bytes(), &report); e != nil {
				t.Fatalf("invalid JSON %q: %v", out.String(), e)
			}
			found := false
			for _, check := range report.Checks {
				if check.Code == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.want, report.Checks)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("doctor changed the index")
			}
		})
	}
}

func TestDoctorBusyIndexReturnsBoundedDiagnostic(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sources(id,kind,name,root) VALUES('busy','filesystem','busy','/busy')"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec("ROLLBACK"); _ = db.Close() }()
	start := time.Now()
	var out, stderr bytes.Buffer
	err = cli.Run(context.Background(), []string{"doctor", "--data-dir", dir, "--json"}, &out, &stderr, "test")
	if elapsed := time.Since(start); elapsed > 7*time.Second {
		t.Fatalf("doctor exceeded bounded inspection: %s", elapsed)
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Fatalf("busy-index exit: %v", err)
	}
	var report struct {
		Checks []struct {
			Code string `json:"code"`
		} `json:"checks"`
	}
	if e := json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), e)
	}
	found := false
	for _, check := range report.Checks {
		if check.Code == "index_busy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("busy diagnostic missing: %+v", report.Checks)
	}
}

func TestDoctorReadOnlyPermissionsWhenEnforced(t *testing.T) {
	if os.PathSeparator == '\\' || os.Geteuid() == 0 {
		t.Skip("file permission enforcement unavailable in this environment")
	}
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "findrail.db")
	if err := os.Chmod(path, 0); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	defer os.Chmod(path, 0600)
	var out, stderr bytes.Buffer
	err = cli.Run(context.Background(), []string{"doctor", "--data-dir", dir, "--json"}, &out, &stderr, "test")
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Fatalf("permission diagnostic exit: %v", err)
	}
}
