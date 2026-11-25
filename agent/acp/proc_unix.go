//go:build !windows

package acp

import (
	"os/exec"
	"syscall"
)

// detachControllingTTY puts the child process in a new session (Setsid: true)
// so it has no controlling terminal (/dev/tty). This prevents CLI tools like
// `agy` from opening interactive browser OAuth flows when silent auth fails.
// Note: do not set Setpgid together with Setsid, as setsid() already creates
// a new process group and setpgid() after setsid() fails with EPERM.
func detachControllingTTY(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = false
	cmd.SysProcAttr.Setsid = true
}
