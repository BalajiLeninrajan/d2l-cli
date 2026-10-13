// Package cli parses the command line and runs the non-TUI commands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/session"
	"github.com/BalajiLeninrajan/d2l-cli/internal/tui"
)

// Exit codes.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
	exitIntr  = 130
)

const loginHint = "Run d2l login."

const usage = `d2l is a terminal client for D2L Brightspace.

Usage:
  d2l                      browse courses and content
  d2l login [flags]        log in and save the session
  d2l logout [--all]       forget the session
  d2l sync [flags] [FILTER...]
                           download course files into the download dir
  d2l config [flags]       show or edit the config file
  d2l version              print the version
  d2l help                 show this help

Login flags:
  (none)                   open your default browser on Learn in a separate
                           window and save the session once you log in
  -i, --interactive        show the window right away instead of trying a
                           hidden renewal with the saved login profile first
  --cookie -               paste cookies copied from a logged-in browser
                           ("d2lSessionVal=...; d2lSecureSessionVal=...")

Logout flags:
  --all                    also delete the login browser profiles

Sync flags:
  --all                    sync every enrollment, not just current and pinned
  -n, --dry-run            list what would be downloaded
  FILTER                   keep courses whose name or code contains FILTER

Config flags:
  --edit                   open the config in $EDITOR
  --path                   print the config path without creating the file

Keys in the courses list:
  enter  open course          f  search the listed courses
  d      download its files   w  open it on Learn
  a      show all courses     r  reload
  q      quit

Keys in a course:
  1-4, tab   switch between Content, Announcements, Assignments, Grades
             (shift+tab goes back)
  enter      open a file, folder, announcement, assignment or grade comment
  d          download the file or folder, or save an announcement's attachments
  o          open an announcement's first attachment
  w          open the file, folder or assignment on Learn
  f          search this course
  r          reload
  esc        go back
  q          close this screen

Press / in any list to filter it.
`

// Main runs the command named by args and returns the process exit code.
func Main(args []string) int {
	if len(args) == 0 {
		return runTUI()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "login":
		return runLogin(rest)
	case "logout":
		return runLogout(rest)
	case "sync":
		return runSync(rest)
	case "config":
		return runConfig(rest)
	case "version", "--version":
		fmt.Println(version())
		return exitOK
	case "help", "-h", "--help":
		fmt.Print(usage)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "d2l: unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		return exitUsage
	}
}

func runTUI() int {
	cfg, client, code := loadClient()
	if client == nil {
		return code
	}
	if err := tui.Run(cfg, client); err != nil {
		if isUnauthorized(err) {
			fmt.Fprintln(os.Stderr, "Session expired. "+loginHint)
		} else {
			fmt.Fprintln(os.Stderr, "d2l:", err)
		}
		return exitFail
	}
	return exitOK
}

// loadClient loads the config and saved session. On failure it prints why and
// returns a nil client with the exit code to use.
func loadClient() (*config.Config, *d2l.Client, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "d2l: loading %s: %v\n", config.Path(), err)
		return nil, nil, exitFail
	}
	sess, err := session.Load()
	if errors.Is(err, session.ErrNoSession) {
		fmt.Fprintln(os.Stderr, "Not logged in. "+loginHint)
		return nil, nil, exitFail
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "d2l:", err)
		return nil, nil, exitFail
	}
	base := sess.BaseURL
	if base == "" {
		base = cfg.BaseURL
	}
	client, err := d2l.New(base, sess.HTTPCookies())
	if err != nil {
		fmt.Fprintf(os.Stderr, "d2l: bad base URL %q: %v\n", base, err)
		return nil, nil, exitFail
	}
	return cfg, client, exitOK
}

// newFlagSet returns a FlagSet whose errors and -h output go to stderr.
func newFlagSet(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: d2l %s %s\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args with flags allowed anywhere, so "d2l sync cs135 -n"
// works. It returns the positional arguments, or an exit code when parsing
// stops the command.
func parse(fs *flag.FlagSet, args []string) ([]string, int, bool) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, exitOK, false
			}
			return nil, exitUsage, false
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, 0, true
		}
		// flag.Parse swallows "--" and stops; everything after it is positional.
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(pos, rest...), 0, true
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// buildVersion is set by release builds with -ldflags -X.
var buildVersion string

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// isUnauthorized reports whether err means the session is no longer valid.
func isUnauthorized(err error) bool { return errors.Is(err, d2l.ErrUnauthorized) }

// lineWriter serializes whole lines from concurrent goroutines.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, format+"\n", args...)
}
