package cli

import (
	"fmt"
	"os"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
)

func runConfig(args []string) int {
	fs := newFlagSet("config", "[--edit] [--path]")
	edit := fs.Bool("edit", false, "open the config in $EDITOR")
	pathOnly := fs.Bool("path", false, "print the config path without creating the file")
	pos, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(os.Stderr, "d2l config: unexpected argument %q\n", pos[0])
		return exitUsage
	}
	if *pathOnly && !*edit {
		fmt.Println(config.Path())
		return exitOK
	}
	p, err := config.EnsureFile()
	if err != nil {
		fmt.Fprintln(os.Stderr, "d2l: creating config:", err)
		return exitFail
	}
	if !*edit {
		fmt.Println(p)
		return exitOK
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = files.DefaultEditor
	}
	// Through the shell so EDITOR values with arguments ("code --wait") work.
	cmd := files.ShellCommand(editor, p)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "d2l: running %s: %v\n", editor, err)
		return exitFail
	}
	return exitOK
}
