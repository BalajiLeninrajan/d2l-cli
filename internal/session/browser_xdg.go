//go:build !darwin && !windows

package session

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// xdgDefault returns the default browser's desktop file ID. A variable so
// tests can stub it.
var xdgDefault = func() (string, error) {
	out, err := exec.Command("xdg-settings", "get", "default-web-browser").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		out, err = exec.Command("xdg-mime", "query", "default", "x-scheme-handler/https").Output()
	}
	id := strings.TrimSpace(string(out))
	if err != nil || id == "" {
		return "", errors.New("xdg-settings reports no default browser")
	}
	return id, nil
}

func defaultBrowser() (*Browser, error) {
	id, err := xdgDefault()
	if err != nil {
		return nil, err
	}
	path := findDesktopFile(id)
	if path == "" {
		return nil, fmt.Errorf("no desktop file for %s", id)
	}
	argv, err := desktopExec(path)
	if err != nil {
		return nil, err
	}
	return newBrowser(id, argv), nil
}

// applicationDirs lists where desktop files live, most specific first.
func applicationDirs() []string {
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	dirs := []string{filepath.Join(dataHome, "applications")}
	for d := range strings.SplitSeq(dataDirs, ":") {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "applications"))
		}
	}
	// Flatpak and Snap exports are usually in XDG_DATA_DIRS already, but not
	// when d2l runs outside a desktop session.
	return append(dirs,
		filepath.Join(dataHome, "flatpak", "exports", "share", "applications"),
		"/var/lib/flatpak/exports/share/applications",
		"/var/lib/snapd/desktop/applications",
	)
}

func findDesktopFile(id string) string {
	for _, d := range applicationDirs() {
		p := filepath.Join(d, id)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// desktopExec returns the Exec command of the [Desktop Entry] group, without
// field codes like %u and Flatpak's @@u ... @@ file-forwarding markers.
func desktopExec(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	inEntry := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		value, ok := strings.CutPrefix(line, "Exec=")
		if !inEntry || !ok {
			continue
		}
		var argv []string
		for _, a := range splitCommand(value) {
			if strings.HasPrefix(a, "%") || a == "@@" || a == "@@u" || a == "--file-forwarding" {
				continue
			}
			argv = append(argv, a)
		}
		if len(argv) == 0 {
			break
		}
		return argv, nil
	}
	return nil, fmt.Errorf("%s has no Exec line", path)
}

func installedCandidates() [][]string {
	var out [][]string
	for _, name := range []string{
		"chromium", "chromium-browser", "chromium-freeworld",
		"google-chrome", "google-chrome-stable", "brave-browser", "brave",
		"microsoft-edge", "microsoft-edge-stable", "vivaldi",
		"firefox", "zen", "zen-browser", "librewolf", "floorp",
	} {
		out = append(out, []string{name})
	}
	for _, id := range []string{
		"org.chromium.Chromium", "com.google.Chrome", "com.brave.Browser", "com.microsoft.Edge",
		"org.mozilla.firefox", "app.zen_browser.zen", "io.gitlab.librewolf-community", "one.ablaze.floorp",
	} {
		out = append(out, []string{"flatpak", "run", id})
	}
	return out
}
