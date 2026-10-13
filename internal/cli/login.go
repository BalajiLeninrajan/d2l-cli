package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/session"
)

func runLogin(args []string) int {
	fs := newFlagSet("login", "[-i|--interactive] [--cookie HEADER]")
	var interactive bool
	fs.BoolVar(&interactive, "i", false, "open the browser window right away instead of trying a hidden renewal first")
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
	if *cookie != "" {
		sess, err = session.FromHeader(cfg.BaseURL, *cookie)
	} else {
		sess, err = session.Login(ctx, session.LoginOptions{
			BaseURL:     cfg.BaseURL,
			Browser:     cfg.Browser,
			Interactive: interactive,
			Validate:    validate,
			Logf:        logf,
		})
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
				fmt.Fprintln(os.Stderr, "The browser finished login but Brightspace rejected the captured session, so nothing was saved. Try d2l login -i.")
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
	all := fs.Bool("all", false, "also delete the browser profiles d2l logs in with")
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
	if err := session.DeleteProfiles(); err != nil {
		fmt.Fprintln(os.Stderr, "d2l: deleting browser profiles:", err)
		return exitFail
	}
	fmt.Println("Logged out and deleted the login browser profiles.")
	return exitOK
}
