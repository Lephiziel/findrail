//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package connectorkit

import (
	"os/exec"
)

type otherProcessTree struct{ cmd *exec.Cmd }

func configureProcess(*exec.Cmd) {}
func attachProcessTree(cmd *exec.Cmd, publish func(processTree)) (processTree, error) {
	tree := &otherProcessTree{cmd: cmd}
	publish(tree)
	return tree, nil
}
func (p *otherProcessTree) stop() error { return p.cmd.Process.Kill() }
func (p *otherProcessTree) cleanup()    { _ = p.stop() }
