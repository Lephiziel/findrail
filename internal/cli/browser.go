package cli

import (
	"fmt"
	"os/exec"
	"runtime"
)

func browserCommand(goos, url string) (string, []string, error) {
	switch goos {
	case "linux":
		return "xdg-open", []string{url}, nil
	case "darwin":
		return "open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	default:
		return "", nil, fmt.Errorf("automatic browser opening is unsupported on %s", goos)
	}
}

// openBrowser starts the OS URL handler directly, without a shell. Starting a
// handler is an attempt, not a guarantee that a browser window will appear.
func openBrowser(url string) error {
	name, args, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Some handlers stay alive with the browser; never block local search waiting
	// for them. Reap the process when it eventually exits.
	go func() { _ = cmd.Wait() }()
	return nil
}
