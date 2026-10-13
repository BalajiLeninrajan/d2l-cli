//go:build !darwin && !windows

package session

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const zenDesktop = `[Desktop Entry]
Name=Zen Browser
Exec=/usr/bin/flatpak run --branch=stable --arch=x86_64 --command=launch-script.sh --file-forwarding app.zen_browser.zen @@u %u @@

[Desktop Action new-private-window]
Exec=/usr/bin/flatpak run --command=launch-script.sh app.zen_browser.zen --private-window @@u %u @@
`

func TestDefaultBrowserFromDesktopFile(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	os.MkdirAll(filepath.Join(data, "applications"), 0o755)
	os.WriteFile(filepath.Join(data, "applications", "app.zen_browser.zen.desktop"), []byte(zenDesktop), 0o644)

	orig := xdgDefault
	t.Cleanup(func() { xdgDefault = orig })
	xdgDefault = func() (string, error) { return "app.zen_browser.zen.desktop", nil }

	b, err := defaultBrowser()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/flatpak", "run", "--branch=stable", "--arch=x86_64", "--command=launch-script.sh", "app.zen_browser.zen"}
	if !slices.Equal(b.Argv, want) || b.Family != FamilyFirefox || b.Flatpak != "app.zen_browser.zen" {
		t.Fatalf("got %+v", b)
	}

	xdgDefault = func() (string, error) { return "missing.desktop", nil }
	if _, err := defaultBrowser(); err == nil {
		t.Fatal("expected an error for a missing desktop file")
	}
}
