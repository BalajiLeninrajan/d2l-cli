package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
)

func runClean(args []string) int {
	flags := newFlagSet("clean", "")
	pos, code, ok := parse(flags, args)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(os.Stderr, "d2l clean: unexpected argument %q\n", pos[0])
		return exitUsage
	}
	dir := config.CacheDir()
	size, err := dirSize(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Println("The cache is empty.")
		return exitOK
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "d2l: removing cache:", err)
		return exitFail
	}
	fmt.Printf("Removed %s (%s).\n", dir, formatSize(size))
	return exitOK
}

// dirSize adds up the sizes of the regular files under dir.
func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n, err
}

func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f, i := float64(n)/unit, 0
	for f >= unit && i < 3 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %ciB", f, "KMGT"[i])
}
