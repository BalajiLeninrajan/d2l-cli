//go:build !windows

package session

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the browser in its own group, so killing it also
// kills its helpers and, for Flatpak, the sandboxed browser.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
