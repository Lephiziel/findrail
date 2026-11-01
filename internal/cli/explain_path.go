package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ignorepolicy"
)

type pathsFlag []string

func (p *pathsFlag) String() string     { return strings.Join(*p, ",") }
func (p *pathsFlag) Set(v string) error { *p = append(*p, v); return nil }

func runExplainPath(ctx context.Context, args []string, out, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(out, "Usage: findrail explain-path ROOT --path RELATIVE [--path RELATIVE ...] [--json] [--show-rule]")
		return err
	}
	if len(args) == 0 {
		return errors.New("explain-path requires ROOT and --path")
	}
	root := args[0]
	opts := append([]string(nil), args[1:]...)
	fs := flag.NewFlagSet("explain-path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var paths pathsFlag
	fs.Var(&paths, "path", "root-relative POSIX path (repeatable)")
	dataDir := fs.String("data-dir", "", "index directory for hard-exclusion explanation only; never opened")
	jsonOut := fs.Bool("json", false, "output versioned JSON")
	show := fs.Bool("show-rule", false, "include matched rule text")
	if err := fs.Parse(opts); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || len(paths) == 0 || len(paths) > 100 {
		return errors.New("explain-path requires ROOT and 1–100 --path values")
	}
	for _, path := range paths {
		if err := ignorepolicy.ValidatePath(path); err != nil {
			return err
		}
	}
	type row struct {
		Path        string `json:"path"`
		Status      string `json:"status"`
		Reason      string `json:"reason,omitempty"`
		ErrorCode   string `json:"error_code,omitempty"`
		Fingerprint string `json:"policy_fingerprint,omitempty"`
		Rule        string `json:"rule,omitempty"`
		Line        int    `json:"line,omitempty"`
		Ancestor    string `json:"ancestor,omitempty"`
	}
	rows := make([]row, 0, len(paths))
	present := false
	var policyFailure error
	defaultIndex, err := config.DataDir(*dataDir)
	if err != nil {
		return errors.New("cannot resolve default index location")
	}
	decisions, err := filesystem.ExplainMany(ctx, root, paths, *show, defaultIndex)
	if err != nil {
		var pe *ignorepolicy.Error
		if !errors.As(err, &pe) || pe.Code == "unsafe_path" || pe.Code == "too_many_path_segments" {
			return err
		}
		policyFailure = err
		present = true
		for _, requested := range paths {
			rows = append(rows, row{Path: requested, Status: "policy_error", ErrorCode: pe.Code, Line: pe.Line})
		}
	} else {
		for i, d := range decisions {
			path := paths[i]
			present = present || d.Fingerprint != ""
			status, reason := d.Reason, ""
			if strings.HasPrefix(status, "excluded_builtin:") {
				status, reason = "excluded_builtin", strings.TrimPrefix(status, "excluded_builtin:")
			}
			rows = append(rows, row{Path: path, Status: status, Reason: reason, Fingerprint: d.Fingerprint, Rule: d.Rule, Line: d.Line, Ancestor: d.Ancestor})
		}
	}
	if *jsonOut {
		b, err := json.Marshal(struct {
			Version       int   `json:"version"`
			PolicyPresent bool  `json:"policy_present"`
			Results       []row `json:"results"`
		}{1, present, rows})
		if err != nil {
			return err
		}
		if len(b) > 256<<10 {
			return errors.New("explanation output limit exceeded")
		}
		b = append(b, '\n')
		_, err = out.Write(b)
		if err != nil {
			return err
		}
		return policyFailure
	}
	var text bytes.Buffer
	if policyFailure != nil {
		if _, err := fmt.Fprintln(&text, "Policy: error"); err != nil {
			return err
		}
	} else if present {
		if _, err := fmt.Fprintf(&text, "Policy: present (%s)\n", rows[0].Fingerprint); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(&text, "Policy: absent"); err != nil {
			return err
		}
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(&text, "%s: %s", r.Path, r.Status); err != nil {
			return err
		}
		if r.ErrorCode != "" {
			if _, err := fmt.Fprintf(&text, " (code %s", r.ErrorCode); err != nil {
				return err
			}
			if r.Line > 0 {
				if _, err := fmt.Fprintf(&text, ", line %d", r.Line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(&text, ")"); err != nil {
				return err
			}
		}
		if r.Line > 0 && r.ErrorCode == "" {
			if _, err := fmt.Fprintf(&text, " (line %d", r.Line); err != nil {
				return err
			}
			if r.Rule != "" {
				if _, err := fmt.Fprintf(&text, ": %s", safe(r.Rule)); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(&text, ")"); err != nil {
				return err
			}
		}
		if r.Ancestor != "" && r.Ancestor != r.Path {
			if _, err := fmt.Fprintf(&text, " (%s)", r.Ancestor); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(&text); err != nil {
			return err
		}
	}
	if text.Len() > 256<<10 {
		return errors.New("explanation output limit exceeded")
	}
	_, err = out.Write(text.Bytes())
	if err != nil {
		return err
	}
	return policyFailure
}
