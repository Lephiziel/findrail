package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type generatedMCPConfig struct {
	MCPServers map[string]generatedMCPServer `json:"mcpServers"`
}

type generatedMCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func runMCPConfig(ctx context.Context, args []string, out, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp-config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: findrail mcp-config --client vscode|claude-desktop --data-dir DIR --source ID [--source ID ...] [--binary PATH]")
		fmt.Fprintln(stderr, "Generate a scoped local stdio MCP client configuration on stdout.")
	}
	client := fs.String("client", "", "client profile: vscode or claude-desktop")
	dataDir := fs.String("data-dir", "", "existing Findrail index directory")
	binary := fs.String("binary", "", "absolute path to the Findrail executable")
	var sources sourceFlags
	fs.Var(&sources, "source", "allowed source ID; repeat at least once")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("mcp-config does not accept positional arguments")
	}
	if *client != "vscode" && *client != "claude-desktop" {
		return errors.New("client must be vscode or claude-desktop")
	}
	if len(sources) == 0 {
		return errors.New("mcp-config requires at least one --source ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := config.DataDir(*dataDir)
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	command := *binary
	if command == "" {
		command, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
		command, err = filepath.Abs(command)
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
	}
	if !filepath.IsAbs(command) {
		return errors.New("binary must be an absolute executable path")
	}
	info, err := os.Stat(command)
	if err != nil {
		return fmt.Errorf("binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("binary must be a regular file")
	}
	// Windows does not expose Unix executable mode bits. Findrail's native
	// Windows command is an .exe; scripts must not acquire shell semantics.
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(command), ".exe") {
		return errors.New("binary must be a Windows .exe file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return errors.New("binary must be executable")
	}
	store, err := sqlite.OpenReadOnly(ctx, dir)
	if err != nil {
		return fmt.Errorf("open read-only index: %w", err)
	}
	defer store.Close()
	registered, err := store.Sources(ctx)
	if err != nil {
		return fmt.Errorf("read sources: %w", err)
	}
	known := make(map[string]struct{}, len(registered))
	for _, source := range registered {
		known[source.ID] = struct{}{}
	}
	for _, id := range sources {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("source %q is not registered in the index", id)
		}
	}
	argsOut := []string{"mcp", "--data-dir", dir}
	for _, id := range sources {
		argsOut = append(argsOut, "--source", id)
	}
	doc := generatedMCPConfig{MCPServers: map[string]generatedMCPServer{"findrail": {Command: command, Args: argsOut}}}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(doc); err != nil {
		return err
	}
	_, err = out.Write(encoded.Bytes())
	return err
}
