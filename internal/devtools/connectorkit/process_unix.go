//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package connectorkit

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixProcessTree struct{ pid int }

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessTree(cmd *exec.Cmd, publish func(processTree)) (processTree, error) {
	tree := &unixProcessTree{pid: cmd.Process.Pid}
	publish(tree)
	return tree, nil
}

func (p *unixProcessTree) stop() error {
	err := syscall.Kill(-p.pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func (p *unixProcessTree) cleanup() { _ = p.stop() }
