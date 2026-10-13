package files

import (
	"os/exec"
	"syscall"
)

// ShellCommand runs a user-configured command through cmd.exe with arg
// appended in quotes. The command line is passed verbatim because Go's
// argument escaping doesn't match what cmd /C expects.
func ShellCommand(command, arg string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + command + ` "` + arg + `""`}
	return cmd
}

func systemOpener(target string) *exec.Cmd {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
}

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detach starts cmd without a console so it outlives the TUI.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup | detachedProcess
}

// DefaultEditor is used when %EDITOR% is unset.
const DefaultEditor = "notepad"
