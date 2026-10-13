package session

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	lsBlock  = regexp.MustCompile(`\{[^{}]*\}`)
	lsScheme = regexp.MustCompile(`LSHandlerURLScheme\s*=\s*"?https"?;`)
	lsRole   = regexp.MustCompile(`LSHandlerRoleAll\s*=\s*"?([^";]+)"?;`)
)

// defaultBrowser reads the https handler from LaunchServices. With no entry,
// the default is Safari.
func defaultBrowser() (*Browser, error) {
	bundle := "com.apple.safari"
	out, err := exec.Command("defaults", "read", "com.apple.LaunchServices/com.apple.launchservices.secure", "LSHandlers").Output()
	if err == nil {
		for _, block := range lsBlock.FindAllString(string(out), -1) {
			if !lsScheme.MatchString(block) {
				continue
			}
			if m := lsRole.FindStringSubmatch(block); m != nil {
				bundle = strings.ToLower(strings.TrimSpace(m[1]))
			}
		}
	}
	app, err := appForBundle(bundle)
	if err != nil {
		return &Browser{Name: bundle}, nil // known name, unknown family: caller falls back
	}
	exe, err := exec.Command("defaults", "read", filepath.Join(app, "Contents", "Info"), "CFBundleExecutable").Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", app, err)
	}
	argv := []string{filepath.Join(app, "Contents", "MacOS", strings.TrimSpace(string(exe)))}
	b := newBrowser(bundle, argv)
	b.Name = strings.TrimSuffix(filepath.Base(app), ".app")
	return b, nil
}

func appForBundle(bundle string) (string, error) {
	out, err := exec.Command("mdfind", fmt.Sprintf("kMDItemCFBundleIdentifier == '%s'", bundle)).Output()
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasSuffix(line, ".app") {
			return line, nil
		}
	}
	return "", fmt.Errorf("no app for %s", bundle)
}

func installedCandidates() [][]string {
	var out [][]string
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
		"/Applications/Firefox.app/Contents/MacOS/firefox",
		"/Applications/Zen.app/Contents/MacOS/zen",
		"/Applications/LibreWolf.app/Contents/MacOS/librewolf",
		"/Applications/Floorp.app/Contents/MacOS/floorp",
	} {
		out = append(out, []string{p})
	}
	return out
}
