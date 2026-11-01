//go:build !linux

package resourcecheck

import "os/exec"

// The producer's measurements are Linux-only. Keep the command package
// buildable on other platforms; only the owned leader can be terminated there.
func configureProcessGroup(*exec.Cmd) {}
func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
