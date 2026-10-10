package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type doctorExit struct{ code int }

func (e doctorExit) Error() string { return "doctor found an index compatibility or readability issue" }
func (e doctorExit) ExitCode() int { return e.code }

type doctorCheck struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	NextAction string `json:"next_action"`
}
type doctorReport struct {
	ReportVersion int           `json:"report_version"`
	Version       string        `json:"version"`
	GOOS          string        `json:"goos"`
	GOARCH        string        `json:"goarch"`
	GoVersion     string        `json:"go_version"`
	Revision      string        `json:"revision,omitempty"`
	Provenance    string        `json:"provenance"`
	DataDirectory string        `json:"data_directory,omitempty"`
	Checks        []doctorCheck `json:"checks"`
}

func runDoctor(ctx context.Context, args []string, out, stderr io.Writer, version string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirArg := fs.String("data-dir", "", "index directory to inspect")
	jsonOut := fs.Bool("json", false, "emit one JSON report")
	showPaths := fs.Bool("show-paths", false, "include local data directory in output")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("doctor does not accept positional arguments")
	}
	dataDir, err := config.DataDir(*dirArg)
	if err != nil {
		return err
	}
	report := doctorReport{ReportVersion: 1, Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), Checks: []doctorCheck{}}
	report.Provenance = "unknown"
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				report.Revision = setting.Value
			case "vcs.modified":
				if setting.Value == "true" {
					report.Provenance = "dirty"
				} else {
					report.Provenance = "clean"
				}
			}
		}
	}
	if *showPaths {
		report.DataDirectory = dataDir
	}
	check := func(id, status, code, message, action string) {
		report.Checks = append(report.Checks, doctorCheck{id, status, code, message, action})
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		check("inspection", "error", "inspection_canceled", "Index inspection was canceled before completion; no repair or migration was attempted.", "Re-run doctor without canceling it.")
	} else {
		info, statErr := os.Stat(dataDir)
		if errors.Is(statErr, os.ErrNotExist) {
			check("index", "ok", "not_initialized", "No index exists yet; this is a normal first-run state.", "Run findrail start to create an index.")
		} else if statErr != nil {
			check("index", "error", "data_directory_unreadable", "The selected data directory cannot be inspected.", "Check the directory path and permissions.")
		} else if !info.IsDir() {
			check("index", "error", "data_directory_not_directory", "The selected data path is not a directory.", "Choose a directory with --data-dir.")
		} else {
			dbInfo, dbErr := os.Stat(filepath.Join(dataDir, "findrail.db"))
			if errors.Is(dbErr, os.ErrNotExist) {
				check("index", "ok", "not_initialized", "No index exists yet; this is a normal first-run state.", "Run findrail start to create an index.")
			} else if dbErr != nil || !dbInfo.Mode().IsRegular() {
				check("index", "error", "index_unreadable", "The index file is unavailable or is not a regular file.", "Check the data directory and file permissions.")
			} else {
				store, openErr := sqlite.OpenReadOnly(ctx, dataDir)
				if openErr != nil {
					code, status, action := "index_unreadable", "error", "Preserve a stopped full-directory backup, then investigate or restore it."
					msg := "The index could not be safely inspected; no repair or migration was attempted."
					lowerError := strings.ToLower(openErr.Error())
					if errors.Is(openErr, context.DeadlineExceeded) || strings.Contains(lowerError, "locked") || strings.Contains(lowerError, "busy") {
						code, msg, action = "index_busy", "The index is busy; inspection stopped without changing it.", "Stop other Findrail writers and retry doctor."
					} else if strings.Contains(lowerError, "unsupported index schema") {
						code, status, action = "schema_unsupported", "error", "Use a compatible Findrail version; do not open a newer index with an older binary."
						parts := strings.Fields(openErr.Error())
						schema := "unknown"
						for i, part := range parts {
							if part == "schema" && i+1 < len(parts) {
								schema = strings.TrimSuffix(parts[i+1], ";")
								break
							}
						}
						if n, parseErr := strconv.Atoi(schema); parseErr == nil && n > 5 {
							msg = "The index uses a newer schema than this binary supports."
						} else {
							msg = "The index uses an older schema and needs an upgrade on first supported write."
							code = "schema_upgrade_needed"
							status = "warning"
							action = "Back up the stopped full data directory, then start with this version to migrate."
						}
					}
					check("schema", status, code, msg, action)
				} else {
					defer store.Close()
					summary, readErr := store.DiagnosticSummary(ctx)
					if readErr != nil {
						check("schema", "error", "index_unreadable", "The supported index could not be read safely.", "Preserve a stopped full-directory backup and investigate.")
					} else {
						message := fmt.Sprintf("Index schema 5 is supported; sources=%d (filesystem=%d, GitHub=%d, other=%d); policies: custom text limits=%d, PDF enabled=%d, DOCX enabled=%d, DOCX disabled=%d.",
							summary.Sources, summary.FilesystemSources, summary.GitHubSources, summary.OtherSources,
							summary.CustomTextLimits, summary.PDFEnabled, summary.DOCXEnabled, summary.DOCXDisabled)
						check("schema", "ok", "schema_supported", message, "No action needed.")
					}
				}
			}
		}
	}
	check("limits", "warning", "alpha_limitations", "Unsigned alpha; local index is plaintext; no OCR; GitHub snapshots refresh manually; legacy folder DOCX remains disabled until configured.", "Review the included INSTALL.txt before use.")
	problem := false
	for _, c := range report.Checks {
		if c.Status == "error" {
			problem = true
		}
	}
	if *jsonOut {
		if err := json.NewEncoder(out).Encode(report); err != nil {
			return err
		}
	} else {
		revision := report.Revision
		if revision == "" {
			revision = "unknown"
		}
		fmt.Fprintf(out, "Findrail doctor report v%d · %s · %s/%s · %s · revision %s (%s)\n", report.ReportVersion, report.Version, report.GOOS, report.GOARCH, report.GoVersion, revision, report.Provenance)
		for _, c := range report.Checks {
			fmt.Fprintf(out, "%s: %s (%s) — %s Next: %s\n", c.ID, c.Status, c.Code, c.Message, c.NextAction)
		}
		if *showPaths {
			fmt.Fprintln(out, "Data directory:", report.DataDirectory)
		}
	}
	if problem {
		return doctorExit{2}
	}
	return nil
}
