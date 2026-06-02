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
	"flag"
	"fmt"
	"os"
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
		chromePath = flag.String("chrome", "", "path to Chrome binary (default: system Chrome)")
		profile    = flag.String("profile", "", "persistent profile dir (default: app config dir)")
		timeout    = flag.Duration("timeout", 3*time.Minute, "max wait for the page to be ready (incl. manual login)")
		headless   = flag.Bool("headless", false, "run without a window — for local file:// exports/testing only; do NOT use against live Upwork (instantly bot-flagged)")
		dryRun     = flag.Bool("dry-run", false, "print the resolved target URL and exit (does not open the browser)")
	)
	flag.Parse()
	args := flag.Args()

	// `login` subcommand: open Upwork visibly, let the user sign in, persist the session.
	if len(args) >= 1 && args[0] == "login" {
		return runLogin(*profile, *chromePath, *timeout)
	}

	if len(args) == 0 {
		return fmt.Errorf("nothing to do.\nUsage:\n" +
			"  upwork-bid-helper login                 # sign in once; saves the session\n" +
			"  upwork-bid-helper <page>                # use myfeed, best, recent, saved\n" +
			"  upwork-bid-helper <upwork-url>          # export a feed/search/job page\n" +
			"  upwork-bid-helper q=\"react native\" ...   # build a search and export")
	}

	target := resolveTarget(args)
	if *dryRun {
		fmt.Println(target)
		return nil
	}
	fmt.Fprintf(os.Stderr, "target: %s\n", target)

	b, err := browser.Launch(browser.Options{ProfileDir: *profile, ChromePath: *chromePath, Headless: *headless})
	if err != nil {
		return err
	}
	defer b.Close()

	page, err := b.NewPage()
	if err != nil {
		return err
	}
	if err := page.Navigate(target); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}

	res, err := waitAndExtract(page, target, *timeout)
	if err != nil {
		return err
	}
	if !res.Exportable() {
		return fmt.Errorf("nothing to export (page type %q, %d jobs) — open a feed, search, or job page", res.PageType, res.Count)
	}

	formats, err := parseFormats(*format)
	if err != nil {
		return err
	}
	written, err := writeOutputs(res, formats, *out)
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
func runLogin(profile, chromePath string, timeout time.Duration) error {
	b, err := browser.Launch(browser.Options{ProfileDir: profile, ChromePath: chromePath})
	if err != nil {
		return err
	}
	defer b.Close()

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
			// Give Chrome a moment to flush cookies to the profile before closing.
			time.Sleep(1500 * time.Millisecond)
			fmt.Fprintf(os.Stderr, "✓ Logged in — session saved.\n")
			fmt.Fprintf(os.Stderr, "Now you can export, e.g.:\n  upwork-bid-helper %q\n", loginURL)
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

// resolveTarget returns the URL to visit: a full URL, a known shortcut
// (recent, feed, …), or one built from key=val search args.
func resolveTarget(args []string) string {
	return search.Resolve(args)
}

// waitAndExtract polls until the page is ready, surfacing login/CAPTCHA to the
// user (the visible window lets them solve it), then runs the extractor.
func waitAndExtract(page *rod.Page, target string, timeout time.Duration) (*model.Result, error) {
	deadline := time.Now().Add(timeout)
	var notedChallenge bool
	renavigated := false

	for time.Now().Before(deadline) {
		switch browser.Probe(page) {
		case browser.StatusLogin, browser.StatusCaptcha:
			if !notedChallenge {
				fmt.Fprintln(os.Stderr, "→ Upwork is asking you to log in / solve a challenge in the browser window. Waiting for you to finish…")
				notedChallenge = true
			}
		case browser.StatusReady:
			// If we had been bounced to login, return to the target once.
			if notedChallenge && !renavigated && !sameURL(page, target) {
				_ = page.Navigate(target)
				renavigated = true
				time.Sleep(1500 * time.Millisecond)
				continue
			}
			res, err := extract.Run(page)
			if err != nil {
				return nil, err
			}
			if res.Exportable() || res.PageType == model.PageUnknown {
				return res, nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	// Last attempt so the user gets a result/error rather than a bare timeout.
	if res, err := extract.Run(page); err == nil {
		return res, nil
	}
	return nil, fmt.Errorf("timed out after %s waiting for the page to be ready", timeout)
}

func sameURL(page *rod.Page, target string) bool {
	info, err := page.Info()
	if err != nil {
		return false
	}
	return strings.Contains(info.URL, strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://"))
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
