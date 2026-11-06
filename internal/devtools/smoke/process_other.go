//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package smoke

import (
	"os"
	"os/exec"
)

type otherProcessTree struct{ process *os.Process }

func configureProcess(*exec.Cmd) {}
func attachProcessTree(cmd *exec.Cmd) (processTree, error) {
	return &otherProcessTree{process: cmd.Process}, nil
}
func stopUnattached(process *os.Process) error { return process.Kill() }
func (p *otherProcessTree) stop() error        { return p.process.Kill() }
func (p *otherProcessTree) cleanup()           { _ = p.stop() }
