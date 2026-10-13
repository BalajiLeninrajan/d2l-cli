package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/session"
)

func runLogin(args []string) int {
	fs := newFlagSet("login", "[--chromium [-i]] [--cookie HEADER]")
	chromium := fs.Bool("chromium", false, "log in through a Chromium window driven by d2l instead of your default browser")
	var interactive bool
	fs.BoolVar(&interactive, "i", false, "with --chromium, open the window right away instead of trying a hidden refresh first")
	fs.BoolVar(&interactive, "interactive", false, "same as -i")
	cookie := fs.String("cookie", "", `session cookies "d2lSessionVal=...; d2lSecureSessionVal=...", or - to read them from stdin`)
	pos, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(os.Stderr, "d2l login: unexpected argument %q\n", pos[0])
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "d2l: loading %s: %v\n", config.Path(), err)
		return exitFail
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *cookie == "-" {
		header, err := readCookie()
		if err != nil {
			fmt.Fprintln(os.Stderr, "d2l login: reading cookies:", err)
			return exitFail
		}
		*cookie = header
	}

	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	var who *d2l.WhoAmI
	validate := func(ctx context.Context, s *session.Session) error {
		w, err := whoAmI(ctx, s)
		if err == nil {
			who = w
		}
		return err
	}

	var sess *session.Session
	switch {
	case *cookie != "":
		sess, err = session.FromHeader(cfg.BaseURL, *cookie)
	case !*chromium && !interactive:
		sess, err = session.LoginDefaultBrowser(ctx, cfg.BaseURL, validate, logf)
		switch {
		case errors.Is(err, session.ErrChromiumDefault):
			logf("Your default browser is Chromium-based, and d2l can't read its cookies. Using a Chromium window instead.")
			sess, err = chromiumLogin(ctx, cfg, false, logf)
		case errors.Is(err, session.ErrNoBrowserStore):
			logf("No profile found for your default browser, so d2l can't read its session. Using a Chromium window instead.")
			sess, err = chromiumLogin(ctx, cfg, false, logf)
		}
	default:
		sess, err = chromiumLogin(ctx, cfg, interactive, logf)
	}
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "Login cancelled.")
			return exitIntr
		}
		fmt.Fprintln(os.Stderr, "d2l login:", err)
		return exitFail
	}

	if who == nil {
		if err := validate(ctx, sess); err != nil {
			switch {
			case isUnauthorized(err) && *cookie != "":
				fmt.Fprintln(os.Stderr, "Brightspace rejected those cookies, so nothing was saved. Copy both cookies again from a tab where you are logged in.")
			case isUnauthorized(err):
				fmt.Fprintln(os.Stderr, "The browser finished login but Brightspace rejected the captured session, so nothing was saved. Try d2l login --chromium -i.")
			default:
				fmt.Fprintf(os.Stderr, "Could not check the session (%v), so nothing was saved.\n", err)
			}
			return exitFail
		}
	}
	if err := sess.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "d2l: saving session:", err)
		return exitFail
	}
	fmt.Printf("Logged in as %s %s (%s)\n", who.FirstName, who.LastName, who.UniqueName)
	return exitOK
}

func chromiumLogin(ctx context.Context, cfg *config.Config, interactive bool, logf func(string, ...any)) (*session.Session, error) {
	return session.Login(ctx, session.LoginOptions{
		BaseURL:     cfg.BaseURL,
		Browser:     cfg.Browser,
		Interactive: interactive,
		Logf:        logf,
	})
}

// readCookie reads the cookie header from stdin, without echo when stdin is
// a terminal, so the session never sits in argv or shell history.
func readCookie() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Paste the cookies and press enter: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func whoAmI(ctx context.Context, sess *session.Session) (*d2l.WhoAmI, error) {
	client, err := d2l.New(sess.BaseURL, sess.HTTPCookies())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return client.WhoAmI(ctx)
}

func runLogout(args []string) int {
	fs := newFlagSet("logout", "[--all]")
	all := fs.Bool("all", false, "also delete the saved browser profile")
	pos, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(os.Stderr, "d2l logout: unexpected argument %q\n", pos[0])
		return exitUsage
	}
	if err := session.Delete(); err != nil {
		fmt.Fprintln(os.Stderr, "d2l: deleting session:", err)
		return exitFail
	}
	if !*all {
		fmt.Println("Logged out.")
		return exitOK
	}
	if err := os.RemoveAll(filepath.Join(config.StateDir(), "browser")); err != nil {
		fmt.Fprintln(os.Stderr, "d2l: deleting browser profile:", err)
		return exitFail
	}
	fmt.Println("Logged out and deleted the saved browser profile.")
	return exitOK
}
