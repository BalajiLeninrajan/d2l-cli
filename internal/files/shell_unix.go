//go:build !windows

package files

import (
	"os/exec"
	"runtime"
	"syscall"
)

// ShellCommand runs a user-configured command with arg appended as "$1", so
// commands with their own arguments (`zathura --fork`, `code --wait`) work
// and arg is never parsed by the shell.
func ShellCommand(command, arg string) *exec.Cmd {
	return exec.Command("sh", "-c", command+` "$1"`, "d2l", arg)
}

func systemOpener(target string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", target)
	}
	return exec.Command("xdg-open", target)
}

// detach starts cmd in its own session so it outlives the TUI.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// DefaultEditor is used when $EDITOR is unset.
const DefaultEditor = "vi"
