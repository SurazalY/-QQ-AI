//go:build !windows

package antigravity

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestDetachControllingTTY(t *testing.T) {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	detachControllingTTY(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil after detachControllingTTY")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Fatal("expected Setsid=true")
	}
	if cmd.SysProcAttr.Setpgid {
		t.Fatal("expected Setpgid=false when Setsid=true to avoid EPERM")
	}
}
