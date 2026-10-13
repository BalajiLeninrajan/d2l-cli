package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
	"github.com/BalajiLeninrajan/d2l-cli/internal/session"
)

type syncJob struct {
	course d2l.Course
	topic  d2l.Topic
	dest   string
}

func runSync(args []string) int {
	fs := newFlagSet("sync", "[--all] [-n|--dry-run] [FILTER...]")
	all := fs.Bool("all", false, "sync every enrollment, not just current and pinned courses")
	var dryRun bool
	fs.BoolVar(&dryRun, "n", false, "list what would be downloaded")
	fs.BoolVar(&dryRun, "dry-run", false, "list what would be downloaded")
	filters, code, ok := parse(fs, args)
	if !ok {
		return code
	}

	cfg, client, code := loadClient()
	if client == nil {
		return code
	}
	// SIGTERM and SIGHUP (a closed terminal) also cancel, so downloads in
	// flight remove their temp files.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	courses, err := client.Courses(ctx)
	if isUnauthorized(err) {
		if fresh := refreshSession(ctx, cfg); fresh != nil {
			client = fresh
			courses, err = client.Courses(ctx)
		}
	}
	if err != nil {
		return reportFatal(ctx, "listing courses", err)
	}
	courses = selectCourses(courses, *all, filters)
	if len(courses) == 0 {
		if len(filters) > 0 {
			fmt.Fprintln(os.Stderr, "No courses match.")
			return exitFail
		}
		fmt.Println("No current or pinned courses. Use --all to sync every enrollment.")
		return exitOK
	}

	out := &lineWriter{w: os.Stdout}
	jobs, unavailable, tocFailed, err := collectJobs(ctx, cfg, client, courses, out)
	if err != nil {
		return reportFatal(ctx, "loading content", err)
	}
	notYet := ""
	if unavailable > 0 {
		notYet = fmt.Sprintf(", %d not available yet", unavailable)
	}

	if dryRun {
		var fresh int
		for _, j := range jobs {
			if files.Fresh(j.dest, j.topic) {
				fresh++
				continue
			}
			out.printf("would download %s", rel(cfg, j.dest))
		}
		fmt.Printf("%d to download, %d up to date, %d failed%s\n", len(jobs)-fresh, fresh, tocFailed, notYet)
		if tocFailed > 0 {
			return exitFail
		}
		return exitOK
	}

	var downloaded, fresh, failed atomic.Int64
	failed.Store(int64(tocFailed))
	// The group context is cancelled only by ErrUnauthorized (or ctrl-c), so
	// one bad file doesn't stop the rest.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.Concurrency)
	for _, j := range jobs {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			skipped, err := files.Download(gctx, client, j.course.ID(), j.topic, j.dest)
			switch {
			case err == nil && skipped:
				fresh.Add(1)
			case err == nil:
				downloaded.Add(1)
				out.printf("↓ %s", rel(cfg, j.dest))
			case isUnauthorized(err):
				return d2l.ErrUnauthorized
			case gctx.Err() != nil:
				// Cancelled because of another failure or ctrl-c; not this file's fault.
			default:
				failed.Add(1)
				out.printf("✗ %s: %v", rel(cfg, j.dest), err)
			}
			return nil
		})
	}
	werr := g.Wait()
	fmt.Printf("%d downloaded, %d up to date, %d failed%s\n", downloaded.Load(), fresh.Load(), failed.Load(), notYet)

	switch {
	case errors.Is(werr, d2l.ErrUnauthorized):
		fmt.Fprintln(os.Stderr, "Session expired. "+loginHint)
		return exitFail
	case ctx.Err() != nil:
		fmt.Fprintln(os.Stderr, "Interrupted.")
		return exitIntr
	case failed.Load() > 0:
		return exitFail
	}
	return exitOK
}

// selectCourses keeps current or pinned courses (every accessible one with
// all), then applies the filters: a course stays if any filter is a
// case-insensitive substring of its name or code.
func selectCourses(courses []d2l.Course, all bool, filters []string) []d2l.Course {
	var out []d2l.Course
	for _, c := range courses {
		if all {
			if !c.Access.CanAccess {
				continue
			}
		} else if !c.Current() && !c.Pinned() {
			continue
		}
		if len(filters) > 0 && !matchesAny(c, filters) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func matchesAny(c d2l.Course, filters []string) bool {
	name, code := strings.ToLower(c.Name()), strings.ToLower(c.Code())
	for _, f := range filters {
		f = strings.ToLower(f)
		if strings.Contains(name, f) || strings.Contains(code, f) {
			return true
		}
	}
	return false
}

// refreshSession renews an expired session in a hidden browser with the
// saved login profile, for when SSO still remembers the user. It returns a
// client for the new session, or nil.
func refreshSession(ctx context.Context, cfg *config.Config) *d2l.Client {
	fmt.Fprintln(os.Stderr, "The session expired. Trying to renew it with the saved login profile...")
	sess, err := session.Login(ctx, session.LoginOptions{
		BaseURL:  cfg.BaseURL,
		Browser:  cfg.Browser,
		Silent:   true,
		Validate: func(ctx context.Context, s *session.Session) error { _, err := whoAmI(ctx, s); return err },
	})
	if err != nil {
		return nil
	}
	client, err := d2l.New(sess.BaseURL, sess.HTTPCookies())
	if err != nil {
		return nil
	}
	if err := sess.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "d2l: saving session:", err)
	}
	return client
}

// collectJobs fetches every course's table of contents concurrently and
// returns the visible, available File topics in course order, and how many
// were left out because Learn will not serve them yet. A course that fails
// to load is reported and counted; ErrUnauthorized aborts.
func collectJobs(ctx context.Context, cfg *config.Config, client *d2l.Client, courses []d2l.Course, out *lineWriter) (jobs []syncJob, unavailable, failedTOCs int, err error) {
	tocs := make([]*d2l.TOC, len(courses))
	var failed atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.Concurrency)
	for i, c := range courses {
		g.Go(func() error {
			toc, err := client.TOC(gctx, c.ID())
			switch {
			case err == nil:
				tocs[i] = toc
			case isUnauthorized(err):
				return d2l.ErrUnauthorized
			case gctx.Err() != nil:
			default:
				failed.Add(1)
				out.printf("✗ %s: %v", c.Name(), err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, 0, err
	}

	now := time.Now()
	seen := map[string]bool{}
	for i, toc := range tocs {
		if toc == nil {
			continue
		}
		c := courses[i]
		for _, e := range files.Entries(toc.Root(), files.CourseDir(cfg, c), nil) {
			if !e.Topic.IsFile() || e.Topic.IsHidden {
				continue
			}
			if !e.Topic.Available(now) {
				unavailable++
				continue
			}
			// Entries gives clashing topics distinct names, but two courses
			// with one name still share a folder. Downloading both would race
			// on the same path, so the first wins. Case is ignored for
			// case-insensitive file systems.
			key := strings.ToLower(e.Dest)
			if seen[key] {
				out.printf("! %s: another topic has the same file name, skipped", rel(cfg, e.Dest))
				continue
			}
			seen[key] = true
			jobs = append(jobs, syncJob{course: c, topic: e.Topic, dest: e.Dest})
		}
	}
	return jobs, unavailable, int(failed.Load()), nil
}

// reportFatal prints an error that stops sync before any downloads.
func reportFatal(ctx context.Context, what string, err error) int {
	switch {
	case isUnauthorized(err):
		fmt.Fprintln(os.Stderr, "Session expired. "+loginHint)
		return exitFail
	case ctx.Err() != nil:
		fmt.Fprintln(os.Stderr, "Interrupted.")
		return exitIntr
	}
	fmt.Fprintf(os.Stderr, "d2l sync: %s: %v\n", what, err)
	return exitFail
}

func rel(cfg *config.Config, p string) string {
	if r, err := filepath.Rel(cfg.DownloadDir, p); err == nil {
		return r
	}
	return p
}
