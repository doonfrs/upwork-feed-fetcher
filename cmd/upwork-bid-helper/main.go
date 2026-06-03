// Command upwork-bid-helper is a console tool that drives a real Chrome to
// export Upwork feed/search results or a single job to JSON / CSV / XML.
//
//	upwork-bid-helper login                      # sign in once; saves the session
//	upwork-bid-helper <page|url>                 # myfeed | best | recent | saved | a full URL
//	upwork-bid-helper q="react native" category=...   # build a search and export
//
// The browser opens visibly; log in once and the persistent profile reuses the
// session on later runs.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"

	"github.com/doonfrs/upwork-bid-helper/internal/browser"
	"github.com/doonfrs/upwork-bid-helper/internal/export"
	"github.com/doonfrs/upwork-bid-helper/internal/extract"
	"github.com/doonfrs/upwork-bid-helper/internal/model"
	"github.com/doonfrs/upwork-bid-helper/internal/search"
)

// holdOpen keeps the visible browser open for manual interaction until Ctrl+C
// (handled by closeOnInterrupt). Shared by --hold across all commands.
func holdOpen() {
	fmt.Fprintln(os.Stderr, "Browser is open for manual testing. Interact in the window; press Ctrl+C here to close.")
	select {} // block forever; Ctrl+C is handled by closeOnInterrupt
}

// closeOnInterrupt shuts the browser down on Ctrl+C so it doesn't leave an
// orphaned Chrome holding the profile lock.
func closeOnInterrupt(b *browser.Browser) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		b.Close()
		os.Exit(130)
	}()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		format     = flag.String("format", "json", "output format: json | csv | xml | all (or comma-separated)")
		out        = flag.String("out", "", "output file (or prefix when multiple formats); default ./upwork-<type>-<ts>")
		output     = flag.String("output", "", "alias for --out")
		chromePath = flag.String("chrome", "", "path to Chrome binary (default: system Chrome)")
		profile    = flag.String("profile", "", "persistent profile dir (default: app config dir)")
		timeout    = flag.Duration("timeout", 90*time.Second, "max wait for the page to load")
		pages      = flag.Int("pages", 1, "how many feed pages to load (clicks \"Load More Jobs\" pages-1 times); applies to feeds and `all`")
		dryRun     = flag.Bool("dry-run", false, "print the resolved target URL and exit (does not open the browser)")
		gui        = flag.Bool("gui", false, "show the browser window during export (disables headless; useful for debugging or pages behind a challenge)")
		hold       = flag.Bool("hold", false, "open a visible browser, navigate, then keep it open for manual testing (Ctrl+C to close; no export)")
	)
	// Go's flag package stops at the first non-flag arg, so `recent --gui` would
	// treat --gui as a positional. Loop the parse to allow flags and the
	// positional arg (a page shortcut, a URL, or `login`) in any order.
	var args []string
	rest := os.Args[1:]
	for {
		if err := flag.CommandLine.Parse(rest); err != nil {
			return err
		}
		rest = flag.Args()
		if len(rest) == 0 {
			break
		}
		args = append(args, rest[0])
		rest = rest[1:]
	}

	// --output is an alias for --out; prefer whichever was set.
	outFile := *out
	if outFile == "" {
		outFile = *output
	}

	pageCount := *pages
	if pageCount < 1 {
		pageCount = 1
	}

	// `login` subcommand: open Upwork visibly, let the user sign in, persist the session.
	if len(args) >= 1 && args[0] == "login" {
		return runLogin(*profile, *chromePath, *timeout, *hold)
	}

	// --hold with no target defaults to the find-work home so you have a page to
	// poke at manually.
	if *hold && len(args) == 0 {
		args = []string{"myfeed"}
	}

	if len(args) == 0 {
		return fmt.Errorf("nothing to do.\nUsage:\n" +
			"  upwork-bid-helper login                  # sign in once; saves the session\n" +
			"  upwork-bid-helper <page>                 # use myfeed, best, recent, saved\n" +
			"  upwork-bid-helper all                    # sweep every feed, merge + dedupe\n" +
			"  upwork-bid-helper <upwork-url>           # export a feed or job page\n" +
			"  upwork-bid-helper --gui recent           # export with a visible window\n" +
			"  upwork-bid-helper --hold recent          # open and keep the window open")
	}

	// `all` sweeps every feed and merges them; otherwise resolve a single target.
	allMode := len(args) == 1 && strings.EqualFold(args[0], "all")
	var target string
	if !allMode {
		t, err := resolveTarget(args)
		if err != nil {
			return err
		}
		target = t
	}
	if *dryRun {
		if allMode {
			for _, f := range allFeeds {
				fmt.Println(f.url)
			}
		} else {
			fmt.Println(target)
		}
		return nil
	}

	// Exports run headless (background, no window) by default; --gui shows the
	// window. --hold always runs visibly so you can interact.
	b, err := browser.Launch(browser.Options{ProfileDir: *profile, ChromePath: *chromePath, Headless: !*gui && !*hold})
	if err != nil {
		return err
	}
	defer b.Close()
	closeOnInterrupt(b)

	page, err := b.NewPage()
	if err != nil {
		return err
	}

	var res *model.Result
	if allMode {
		res, err = exportAll(b, page, *timeout, pageCount)
	} else {
		fmt.Fprintf(os.Stderr, "target: %s\n", target)
		if nerr := page.Navigate(target); nerr != nil {
			return fmt.Errorf("navigate: %w", nerr)
		}
		// --hold: leave the window open for manual testing instead of extracting.
		if *hold {
			holdOpen()
		}
		res, err = waitAndExtract(b, page, *timeout, pageCount)
	}
	if err != nil {
		return err
	}
	if !res.Exportable() {
		return fmt.Errorf("nothing to export (page type %q, %d jobs) — open a feed or job page", res.PageType, res.Count)
	}
	// Refresh the saved session with the (possibly rotated) cookies from this run.
	if err := b.SaveState(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
	}

	formats, err := parseFormats(*format)
	if err != nil {
		return err
	}
	written, err := writeOutputs(res, formats, outFile)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "exported %d job(s) from %s page:\n", count(res), res.PageType)
	for _, p := range written {
		fmt.Println(p)
	}
	return nil
}

// loginURL is an auth-gated page: logged-out users are bounced to a login page,
// so reaching it signed-in is a reliable "you're logged in" signal.
const loginURL = "https://www.upwork.com/nx/find-work/most-recent"

// runLogin opens a visible browser, waits for the user to sign in (and solve any
// challenge), and exits once an authenticated session is detected. The session
// is written to the persistent profile and reused by later runs.
func runLogin(profile, chromePath string, timeout time.Duration, hold bool) error {
	b, err := browser.Launch(browser.Options{ProfileDir: profile, ChromePath: chromePath})
	if err != nil {
		return err
	}
	defer b.Close()
	closeOnInterrupt(b)

	page, err := b.NewPage()
	if err != nil {
		return err
	}
	if err := page.Navigate(loginURL); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}

	fmt.Fprintf(os.Stderr, "A Chrome window opened at Upwork.\n")
	fmt.Fprintf(os.Stderr, "→ Sign in there (and solve any CAPTCHA). I'll detect when you're in and save the session.\n")
	fmt.Fprintf(os.Stderr, "  Profile: %s\n", b.ProfileDir())

	deadline := time.Now().Add(timeout)
	var notedLogin, notedCaptcha bool
	for time.Now().Before(deadline) {
		switch browser.AuthState(page) {
		case browser.AuthIn:
			// Let Upwork finish setting auth cookies, then snapshot the session
			// (incl. the httpOnly/session cookies Chrome would otherwise drop).
			time.Sleep(1500 * time.Millisecond)
			if err := b.SaveState(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
			}
			fmt.Fprintf(os.Stderr, "✓ Logged in — session saved.\n")
			if hold {
				holdOpen()
			}
			fmt.Fprintf(os.Stderr, "Now you can export, e.g.:\n  upwork-bid-helper recent\n")
			return nil
		case browser.AuthLogin:
			if !notedLogin {
				fmt.Fprintf(os.Stderr, "  …waiting for you to sign in.\n")
				notedLogin = true
			}
		case browser.AuthCaptcha:
			if !notedCaptcha {
				fmt.Fprintf(os.Stderr, "  …a challenge is showing — please solve it in the window.\n")
				notedCaptcha = true
			}
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("timed out after %s. If you did sign in, your session is likely already saved — just re-run a command; otherwise run `login` again", timeout)
}

// resolveTarget returns the URL to visit: a full URL or a known page shortcut
// (myfeed, best, recent, saved).
func resolveTarget(args []string) (string, error) {
	return search.Resolve(args)
}

// allFeeds are the find-work feeds the `all` command sweeps and merges. Saved
// Jobs is intentionally excluded — it's a personal bookmark list, not a source
// of new jobs to bid on.
var allFeeds = []struct{ name, url string }{
	{"myfeed", search.URLMyFeed},
	{"best", search.URLBestMatches},
	{"recent", search.URLMostRecent},
}

// exportAll visits each find-work feed in turn and merges their jobs into one
// result, deduplicating by job ID (falling back to UID). A login/challenge wall
// on any feed aborts the run, since it means the session needs refreshing.
func exportAll(b *browser.Browser, page *rod.Page, timeout time.Duration, pages int) (*model.Result, error) {
	combined := &model.Result{PageType: model.PageAll}
	seen := map[string]bool{}
	for _, f := range allFeeds {
		fmt.Fprintf(os.Stderr, "\n=== feed: %s ===\n→ navigating to %s\n", f.name, f.url)
		if err := page.Navigate(f.url); err != nil {
			return nil, fmt.Errorf("navigate %s: %w", f.name, err)
		}
		res, err := waitAndExtract(b, page, timeout, pages)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
		added := 0
		for _, j := range res.Jobs {
			key := j.ID
			if key == "" {
				key = j.UID
			}
			if key != "" {
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			combined.Jobs = append(combined.Jobs, j)
			added++
		}
		fmt.Fprintf(os.Stderr, "= %s: %d jobs extracted, %d new (%d unique total so far)\n",
			f.name, len(res.Jobs), added, len(combined.Jobs))
	}
	combined.Count = len(combined.Jobs)
	return combined, nil
}

// errLoginRequired is returned when an export hits a login/challenge wall.
// The export browser is hidden, so we don't pop a window — we tell the user to
// run `login` (which opens visibly) to refresh the session.
var errLoginRequired = errors.New("login required — run: upwork-bid-helper login")

// waitAndExtract polls until the (hidden) page is ready, then runs the
// extractor. If pages > 1, it clicks "Load More Jobs" to pull additional pages
// before the final extraction. If Upwork shows login/CAPTCHA, it returns
// errLoginRequired.
func waitAndExtract(b *browser.Browser, page *rod.Page, timeout time.Duration, pages int) (*model.Result, error) {
	deadline := time.Now().Add(timeout)
	var readyEmptyAt time.Time // first time we saw a ready page with no jobs
	for time.Now().Before(deadline) {
		switch browser.Probe(page) {
		case browser.StatusLogin, browser.StatusCaptcha:
			return nil, errLoginRequired
		case browser.StatusReady:
			res, err := extract.Run(page)
			if err != nil {
				return nil, err
			}
			if res.Exportable() {
				if info, ierr := page.Info(); ierr == nil {
					fmt.Fprintf(os.Stderr, "  page ready: %s\n  initial extract: %d jobs (pageType=%s)\n",
						info.URL, len(res.Jobs), res.PageType)
				}
				if pages > 1 && loadMoreJobs(b, page, pages-1) {
					if more, err := extract.Run(page); err == nil && more.Exportable() {
						fmt.Fprintf(os.Stderr, "  final extract after load-more: %d jobs\n", len(more.Jobs))
						res = more
					}
				}
				return res, nil
			}
			if res.PageType == model.PageUnknown {
				return res, nil
			}
			// Ready but no jobs: this is a genuinely empty feed (e.g. no saved
			// jobs), not a still-loading one. Give a brief grace for late jobs to
			// appear, then accept the empty result instead of waiting out the
			// full timeout.
			if readyEmptyAt.IsZero() {
				readyEmptyAt = time.Now()
			} else if time.Since(readyEmptyAt) >= 3*time.Second {
				return res, nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	// Last attempt so the user gets a result/error rather than a bare timeout.
	if res, err := extract.Run(page); err == nil {
		return res, nil
	}
	return nil, fmt.Errorf("timed out after %s waiting for the page", timeout)
}

// loadMoreJobs clicks the feed's "Load More Jobs" button up to n times, waiting
// after each click for the job list to grow. It stops early if the button is
// gone (last page) or no new jobs appear.
// loadMoreJobs returns true if it loaded at least one additional page.
func loadMoreJobs(b *browser.Browser, page *rod.Page, n int) bool {
	loaded := false
	for i := 0; i < n; i++ {
		before := jobCount(page)
		fmt.Fprintf(os.Stderr, "  [page %d] looking for \"Load More Jobs\" button (have %d jobs)…\n", i+2, before)
		clicked, err := b.ClickLoadMore(page)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [page %d] load-more click failed: %v\n", i+2, err)
			return loaded
		}
		if !clicked {
			fmt.Fprintf(os.Stderr, "  [page %d] button NOT found — only %d page(s) available; stopping\n", i+2, i+1)
			return loaded
		}
		fmt.Fprintf(os.Stderr, "  [page %d] clicked; waiting for jobs to grow past %d…\n", i+2, before)
		if !waitJobsGrow(page, before, 20*time.Second) {
			fmt.Fprintf(os.Stderr, "  [page %d] clicked but count stayed at %d after 20s; stopping\n", i+2, jobCount(page))
			return loaded
		}
		after := jobCount(page)
		url := ""
		if info, ierr := page.Info(); ierr == nil {
			url = info.URL
		}
		fmt.Fprintf(os.Stderr, "  [page %d] loaded: %d → %d jobs (url: %s)\n", i+2, before, after, url)
		loaded = true
	}
	return loaded
}

// waitJobsGrow polls until the extracted job count exceeds before, or timeout.
func waitJobsGrow(page *rod.Page, before int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if jobCount(page) > before {
			return true
		}
	}
	return false
}

// jobCount returns how many jobs the extractor currently sees on the page.
func jobCount(page *rod.Page) int {
	res, err := extract.Run(page)
	if err != nil {
		return 0
	}
	return len(res.Jobs)
}

func count(res *model.Result) int {
	if len(res.Jobs) > 0 {
		return len(res.Jobs)
	}
	if res.Job != nil {
		return 1
	}
	return 0
}

func parseFormats(s string) ([]export.Format, error) {
	if strings.EqualFold(s, "all") {
		return []export.Format{export.JSON, export.CSV, export.XML}, nil
	}
	var fs []export.Format
	for _, part := range strings.Split(s, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "json":
			fs = append(fs, export.JSON)
		case "csv":
			fs = append(fs, export.CSV)
		case "xml":
			fs = append(fs, export.XML)
		default:
			return nil, fmt.Errorf("unknown format %q (use json, csv, xml, or all)", part)
		}
	}
	if len(fs) == 0 {
		return nil, fmt.Errorf("no output format selected")
	}
	return fs, nil
}

// writeOutputs writes res in each format and returns the file paths written.
func writeOutputs(res *model.Result, formats []export.Format, out string) ([]string, error) {
	prefix := out
	if prefix == "" {
		prefix = fmt.Sprintf("upwork-%s-%s", res.PageType, time.Now().Format("20060102-150405"))
	}
	// If a single format and an explicit filename with extension, honor it as-is.
	explicitFile := out != "" && len(formats) == 1 && filepath.Ext(out) != ""

	var paths []string
	for _, f := range formats {
		path := prefix + "." + f.Ext()
		if explicitFile {
			path = out
		}
		file, err := os.Create(path)
		if err != nil {
			return paths, fmt.Errorf("create %s: %w", path, err)
		}
		if err := export.Write(file, res, f); err != nil {
			file.Close()
			return paths, fmt.Errorf("write %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}
