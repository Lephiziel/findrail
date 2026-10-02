package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DataDir follows platform data-directory conventions. It never points at a
// source directory unless explicitly configured by the caller.
func DataDir(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Findrail"), nil
		}
		return filepath.Join(home, "AppData", "Local", "Findrail"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Findrail"), nil
	default:
		if data := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
			return filepath.Join(data, "findrail"), nil
		}
		return filepath.Join(home, ".local", "share", "findrail"), nil
	}
}
