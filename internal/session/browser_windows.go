package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// regValue runs `reg query` and returns the data of the one REG_SZ value it
// prints.
func regValue(args ...string) (string, error) {
	out, err := exec.Command("reg", append([]string{"query"}, args...)...).Output()
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if _, v, ok := strings.Cut(line, "REG_SZ"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", errors.New("no REG_SZ value")
}

// defaultBrowser reads the https handler's ProgId, then that ProgId's open
// command.
func defaultBrowser() (*Browser, error) {
	progID, err := regValue(`HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, "/v", "ProgId")
	if err != nil {
		return nil, fmt.Errorf("reading the default browser: %w", err)
	}
	cmd, err := regValue(`HKCR\`+progID+`\shell\open\command`, "/ve")
	if err != nil {
		return nil, fmt.Errorf("reading the command for %s: %w", progID, err)
	}
	exe := firstWindowsArg(cmd)
	if exe == "" {
		return nil, fmt.Errorf("empty command for %s", progID)
	}
	b := newBrowser(progID, []string{exe})
	b.Name = strings.TrimSuffix(filepath.Base(exe), ".exe")
	return b, nil
}

// firstWindowsArg returns the program from a command like
// `"C:\Program Files\Mozilla Firefox\firefox.exe" -osint -url "%1"`.
func firstWindowsArg(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if rest, ok := strings.CutPrefix(cmd, `"`); ok {
		exe, _, _ := strings.Cut(rest, `"`)
		return exe
	}
	exe, _, _ := strings.Cut(cmd, " ")
	return exe
}

func installedCandidates() [][]string {
	pf, pf86, local := os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")
	var out [][]string
	for _, p := range []string{
		filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(pf, `BraveSoftware\Brave-Browser\Application\brave.exe`),
		filepath.Join(pf, `Mozilla Firefox\firefox.exe`),
		filepath.Join(pf, `Zen Browser\zen.exe`),
		filepath.Join(pf, `LibreWolf\librewolf.exe`),
	} {
		out = append(out, []string{p})
	}
	return out
}
