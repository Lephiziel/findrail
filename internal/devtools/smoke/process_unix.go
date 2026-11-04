//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package smoke

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixProcessTree struct{ pid int }

func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func attachProcessTree(cmd *exec.Cmd) (processTree, error) {
	return &unixProcessTree{pid: cmd.Process.Pid}, nil
}
func stopUnattached(process *os.Process) error { return stopProcessGroup(process.Pid) }
func (p *unixProcessTree) stop() error         { return stopProcessGroup(p.pid) }
func (p *unixProcessTree) cleanup()            { _ = p.stop() }

func stopProcessGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
