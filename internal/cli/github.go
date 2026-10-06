package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	githubconnector "github.com/Lephiziel/findrail/internal/connectors/github"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type githubCommandResult struct {
	Source      any                        `json:"source"`
	Seen        int                        `json:"seen"`
	Updated     int                        `json:"updated"`
	Unchanged   int                        `json:"unchanged"`
	Removed     int                        `json:"removed"`
	Skipped     int                        `json:"skipped"`
	GitHub      sqlite.GitHubSource        `json:"github"`
	SkipReasons githubconnector.SkipCounts `json:"skip_reasons"`
}

func runGitHub(ctx context.Context, command string, args []string, out, stderr io.Writer) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "directory for Findrail's private index")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall operation deadline, 1s–5m")
	jsonOutput := fs.Bool("json", false, "output one JSON object")
	ref, pathValue := "", ""
	maxBytes := githubconnector.DefaultMaxBytes
	if command == "index-github" {
		fs.StringVar(&ref, "ref", "", "branch, tag, heads/NAME, tags/NAME, or full commit SHA")
		fs.StringVar(&pathValue, "path", "", "relative repository directory")
		fs.Int64Var(&maxBytes, "max-bytes", maxBytes, "maximum bytes per text file, 1–8388608")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%s requires exactly one argument", command)
	}
	if *timeout < time.Second || *timeout > 5*time.Minute {
		return errors.New("timeout must be between 1s and 5m")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var sel githubconnector.Selection
	var expected *sqlite.GitHubSource
	var sourceID string
	dir, err := config.DataDir(*dataDir)
	if err != nil {
		return err
	}
	if command == "index-github" {
		owner, repo, err := githubconnector.ParseRepository(fs.Arg(0))
		if err != nil {
			return err
		}
		sel, err = githubconnector.SelectionFrom(owner, repo, ref, pathValue, maxBytes)
		if err != nil {
			return err
		}
	} else if err := githubconnector.ValidateSourceID(fs.Arg(0)); err != nil {
		return err
	}
	child, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if command == "refresh-github" {
		store, err := sqlite.Open(child, dir)
		if err != nil {
			return fmt.Errorf("open index: %w", err)
		}
		defer store.Close()
		g, err := store.GitHubSource(child, fs.Arg(0))
		if err != nil {
			return fmt.Errorf("GitHub source not found: %w", err)
		}
		expected = &g
		sourceID = g.SourceID
		sel = githubconnector.Selection{Owner: g.Owner, Repo: g.Repo, RefMode: g.RefMode, RefValue: g.RefValue, Path: g.SelectedPath, MaxBytes: g.MaxBytes}
		snapshot, err := githubconnector.NewClient().Prepare(child, sel)
		if err != nil {
			store.RecordGitHubError(context.Background(), g, err.Error())
			return fmt.Errorf("refresh failed; previous snapshot preserved: %w", err)
		}
		if snapshot.Source().ID != sourceID || snapshot.Metadata().RepositoryID != g.RepositoryID {
			store.RecordGitHubError(context.Background(), g, "repository identity changed")
			return errors.New("refresh failed; repository identity changed and previous snapshot was preserved")
		}
		return publishGitHub(child, store, snapshot, expected, *jsonOutput, out)
	}
	snapshot, err := githubconnector.NewClient().Prepare(child, sel)
	if err != nil {
		return fmt.Errorf("prepare GitHub snapshot: %w", err)
	}
	store, err := sqlite.Open(child, dir)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer store.Close()
	if g, e := store.GitHubSource(child, snapshot.Source().ID); e == nil {
		expected = &g
	} else if !errors.Is(e, ingest.ErrSourceGone) {
		return e
	}
	return publishGitHub(child, store, snapshot, expected, *jsonOutput, out)
}

func publishGitHub(ctx context.Context, store *sqlite.Store, snapshot *githubconnector.Snapshot, expected *sqlite.GitHubSource, jsonOutput bool, out io.Writer) error {
	r, g, err := store.PublishGitHub(ctx, snapshot, expected)
	if err != nil {
		return fmt.Errorf("publish GitHub snapshot: %w", err)
	}
	result := githubCommandResult{Source: r.Source, Seen: r.Seen, Updated: r.Updated, Unchanged: r.Unchanged, Removed: r.Removed, Skipped: r.Skipped, GitHub: g, SkipReasons: snapshot.SkipCounts()}
	if jsonOutput {
		return json.NewEncoder(out).Encode(result)
	}
	_, err = fmt.Fprintf(out, "Indexed %s at %.12s: %d documents, %d updated, %d unchanged, %d removed, %d skipped.\nSource: %s\n", r.Source.Name, g.SHA, r.Seen, r.Updated, r.Unchanged, r.Removed, r.Skipped, r.Source.ID)
	return err
}
