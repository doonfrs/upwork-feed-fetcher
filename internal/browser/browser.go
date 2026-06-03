// Package browser launches and controls a real Chrome via go-rod, using a
// persistent profile so the logged-in Upwork session is reused across runs.
package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Status is the high-level state of a loaded page, used to decide whether the
// window must be surfaced for the human (login / CAPTCHA) or is ready to scrape.
type Status string

const (
	StatusReady   Status = "ready"   // window.__NUXT__ is present; safe to extract
	StatusLogin   Status = "login"   // redirected to a login page
	StatusCaptcha Status = "captcha" // Cloudflare / PerimeterX challenge visible
	StatusLoading Status = "loading" // not ready yet
)

// Options configures a browser launch.
type Options struct {
	ProfileDir string // persistent user-data-dir; defaults to the app config dir
	ChromePath string // explicit Chrome binary; defaults to the system Chrome
	// StateFile is a JSON file of saved cookies (incl. session/httpOnly ones that
	// Chrome drops on close). Restored on launch; written by SaveState. Empty =>
	// derive a path from the profile dir.
	StateFile string
	// Headless runs Chrome in background mode (no window). Exports use this; with
	// the restored session cookies Upwork serves the authenticated pages fine.
	// `login` runs headed so the user can sign in.
	Headless bool
}

// Browser wraps a launched Chrome and its launcher for clean teardown.
type Browser struct {
	launcher   *launcher.Launcher
	rod        *rod.Browser
	profileDir string
	stateFile  string
	userAgent  string // UA override applied to new pages (HeadlessChrome stripped)
}

// DefaultProfileDir returns the app-owned persistent profile directory.
func DefaultProfileDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base, _ = os.UserHomeDir()
	}
	return filepath.Join(base, "upwork-feed-fetcher", "profile")
}

// Launch starts Chrome and connects to it.
func Launch(opts Options) (*Browser, error) {
	profile := opts.ProfileDir
	if profile == "" {
		profile = DefaultProfileDir()
	}
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return nil, fmt.Errorf("create profile dir: %w", err)
	}

	// Self-heal: a previous run that didn't shut down cleanly can leave a Chrome
	// holding the profile (a stale SingletonLock), which aborts new launches.
	reapStaleProfile(profile)

	l := launcher.New().
		UserDataDir(profile).
		Headless(opts.Headless).
		Leakless(false). // avoid the AV-flagged helper binary; we close Chrome ourselves
		Set("disable-blink-features", "AutomationControlled").
		Set("no-sandbox")

	if bin := opts.ChromePath; bin != "" {
		l = l.Bin(bin)
	} else if path, ok := launcher.LookPath(); ok {
		l = l.Bin(path) // prefer the user's real Chrome over a managed download
	}

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("launch chrome: %w (is Chrome installed? profile in use by another window?)", err)
	}

	// Record the Chrome PID so a later run can reap us if we don't shut down
	// cleanly. This is the cross-platform self-heal signal: Windows Chrome never
	// writes the POSIX SingletonLock symlink, so we cannot rely on it there.
	writeOwnerPID(profile, l.PID())

	// NoDefaultDevice: rod otherwise emulates devices.LaptopWithMDPIScreen on
	// every page, which forces a macOS/Chrome-114 User-Agent. On Windows that
	// UA contradicts navigator.platform (Win32) and is stale — a fingerprint
	// mismatch that makes Cloudflare's "Just a moment…" challenge loop forever.
	// We keep Chrome's own native UA instead.
	b := rod.New().ControlURL(controlURL).NoDefaultDevice()
	if err := b.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("connect to chrome: %w", err)
	}

	stateFile := opts.StateFile
	if stateFile == "" {
		stateFile = profile + ".cookies.json"
	}
	br := &Browser{launcher: l, rod: b, profileDir: profile, stateFile: stateFile}

	// Headless Chrome advertises "HeadlessChrome/<ver>" in its User-Agent, which
	// Cloudflare treats as a bot. Mirror the real UA with that token rewritten to
	// "Chrome" so headless exports look like an ordinary browser. Headed UAs have
	// no such token, so this is a no-op there.
	if v, err := (proto.BrowserGetVersion{}).Call(b); err == nil {
		br.userAgent = strings.Replace(v.UserAgent, "HeadlessChrome", "Chrome", 1)
	}

	// Restore cookies (incl. the session/httpOnly auth cookies Chrome drops on
	// close) before any navigation, so the logged-in session is reused.
	if err := br.loadState(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not restore saved session: %v\n", err)
	}
	return br, nil
}

// NewPage opens a blank page with the de-headlessed User-Agent applied before
// any navigation, so the very first (Cloudflare-gated) document request carries
// it. AcceptLanguage is set to a realistic value; Platform is left native so it
// stays consistent with navigator.platform across OSes.
func (b *Browser) NewPage() (*rod.Page, error) {
	page, err := b.rod.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, err
	}
	if b.userAgent != "" {
		if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
			UserAgent:      b.userAgent,
			AcceptLanguage: "en-US,en",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not set user agent: %v\n", err)
		}
	}
	return page, nil
}

// ClickLoadMore clicks the feed "Load More Jobs" button to fetch the next page.
// It returns false (no error) when the button is absent, which means there are
// no further pages.
func (b *Browser) ClickLoadMore(page *rod.Page) (bool, error) {
	btn, err := page.Timeout(5 * time.Second).Element(`[data-test="load-more-button"]`)
	if err != nil || btn == nil {
		return false, nil // no button => only one page
	}
	btn = btn.CancelTimeout()
	_ = btn.ScrollIntoView()
	if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return false, fmt.Errorf("click load-more: %w", err)
	}
	return true, nil
}

// Close shuts the browser down and reaps the Chrome process.
func (b *Browser) Close() {
	if b.rod != nil {
		_ = b.rod.Close()
	}
	if b.launcher != nil {
		b.launcher.Kill()
	}
	// Clean shutdown: drop the owner PID so the next run doesn't try to reap a
	// dead (possibly recycled) PID.
	if b.profileDir != "" {
		_ = os.Remove(ownerPIDFile(b.profileDir))
	}
}

// SaveState writes the browser's current cookies (including session/httpOnly
// ones) to the state file, so the next run can restore the logged-in session.
// Call this only when a good session is in hand (after login / a successful run).
func (b *Browser) SaveState() error {
	if b.stateFile == "" {
		return nil
	}
	cookies, err := b.rod.GetCookies()
	if err != nil {
		return fmt.Errorf("get cookies: %w", err)
	}
	if len(cookies) == 0 {
		// Don't clobber a previously saved session with an empty set (e.g. a
		// file:// run, which has no cookies).
		return nil
	}
	data, err := json.MarshalIndent(cookies, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(b.stateFile, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", b.stateFile, err)
	}
	return nil
}

// loadState restores cookies saved by SaveState. A missing file is not an error.
func (b *Browser) loadState() error {
	if b.stateFile == "" {
		return nil
	}
	data, err := os.ReadFile(b.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cookies []*proto.NetworkCookie
	if err := json.Unmarshal(data, &cookies); err != nil {
		return fmt.Errorf("parse %s: %w", b.stateFile, err)
	}
	if len(cookies) == 0 {
		return nil
	}
	return b.rod.SetCookies(cookiesToParams(cookies))
}

// cookiesToParams converts stored cookies into settable params. Session cookies
// (Session=true) are restored with no expiry so they stay session-scoped.
func cookiesToParams(cs []*proto.NetworkCookie) []*proto.NetworkCookieParam {
	params := make([]*proto.NetworkCookieParam, 0, len(cs))
	for _, c := range cs {
		p := &proto.NetworkCookieParam{
			Name:         c.Name,
			Value:        c.Value,
			Domain:       c.Domain,
			Path:         c.Path,
			Secure:       c.Secure,
			HTTPOnly:     c.HTTPOnly,
			SameSite:     c.SameSite,
			Priority:     c.Priority,
			SameParty:    c.SameParty,
			SourceScheme: c.SourceScheme,
		}
		if !c.Session && c.Expires > 0 {
			p.Expires = c.Expires
		}
		params = append(params, p)
	}
	return params
}

// DefaultStateFile returns the cookie state file path for the default profile.
func DefaultStateFile() string { return DefaultProfileDir() + ".cookies.json" }

// ownerPIDFile is where we record the launched Chrome's PID, so a later run can
// reap it if this one dies without a clean Close.
func ownerPIDFile(profile string) string { return filepath.Join(profile, ".ubh-chrome.pid") }

// writeOwnerPID records the launched Chrome PID under the profile. Best effort:
// a failure only means a future crash won't be auto-reaped.
func writeOwnerPID(profile string, pid int) {
	if pid > 0 {
		_ = os.WriteFile(ownerPIDFile(profile), []byte(strconv.Itoa(pid)), 0o644)
	}
}

// reapStaleProfile kills a leftover Chrome that still holds our app-owned
// profile (from a prior run that didn't exit cleanly) and removes the Singleton
// lock files, so a fresh launch can proceed. Safe because the profile is
// dedicated to this tool: any Chrome bound to it is one of ours.
func reapStaleProfile(profile string) {
	if pid := ownerPID(profile); pid > 0 && chromeOwnsProfile(pid, profile) {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
			time.Sleep(250 * time.Millisecond) // let children exit and release the socket
		}
	}
	_ = os.Remove(ownerPIDFile(profile))
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(profile, name))
	}
}

// ownerPID returns the PID of the Chrome that last claimed this profile. It
// prefers the PID file we write on launch (the only signal available on
// Windows, where Chrome writes no SingletonLock), falling back to the POSIX
// SingletonLock symlink for a Chrome we didn't launch. Returns 0 if neither
// is present/readable.
func ownerPID(profile string) int {
	if data, err := os.ReadFile(ownerPIDFile(profile)); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			return pid
		}
	}
	// Fallback: parse the PID from the SingletonLock symlink (target form
	// "<hostname>-<pid>"). POSIX only; absent on Windows.
	target, err := os.Readlink(filepath.Join(profile, "SingletonLock"))
	if err != nil {
		return 0
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return 0
	}
	pid, _ := strconv.Atoi(target[i+1:])
	return pid
}

// chromeOwnsProfile reports whether pid is a live Chrome process bound to
// profile. On Linux it verifies via /proc to avoid killing a recycled PID; on
// other platforms it trusts the lock (best effort).
func chromeOwnsProfile(pid int, profile string) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return runtime.GOOS != "linux" // dead on Linux; unverifiable elsewhere
	}
	cmd := string(bytes.ReplaceAll(data, []byte{0}, []byte{' '}))
	return strings.Contains(cmd, "chrome") && strings.Contains(cmd, profile)
}

// statusJS classifies the page state from within the page.
const statusJS = `() => {
  const p = location.pathname;
  if (/\/ab\/account-security\/login|\/login\b/.test(p)) return 'login';
  if (document.querySelector('.cf-turnstile, iframe[src*="challenges.cloudflare.com"], [data-sitekey]')) return 'captcha';
  if (document.querySelector('#px-captcha, [id^="px-captcha"], iframe[src*="captcha"]')) return 'captcha';
  const body = document.body ? document.body.innerText.slice(0, 2000) : '';
  if (/press\s*&\s*hold/i.test(body)) return 'captcha';
  if (window.__NUXT__) return 'ready';
  return 'loading';
}`

// Probe classifies the current page state (login / captcha / ready / loading).
func Probe(page *rod.Page) Status {
	obj, err := page.Eval(statusJS)
	if err != nil {
		return StatusLoading
	}
	switch Status(obj.Value.Str()) {
	case StatusLogin:
		return StatusLogin
	case StatusCaptcha:
		return StatusCaptcha
	case StatusReady:
		return StatusReady
	default:
		return StatusLoading
	}
}

// Auth is the authentication state observed on an auth-gated Upwork page.
type Auth string

const (
	AuthIn      Auth = "in"      // signed in (an /nx/ app route rendered)
	AuthLogin   Auth = "login"   // on a login / signup / account-security page
	AuthCaptcha Auth = "captcha" // a challenge is blocking
	AuthUnknown Auth = "unknown" // still loading / indeterminate
)

// authJS runs on an auth-gated route. Logged-out users get bounced to a login
// page, so reaching an /nx/ app route with the SPA initialized means signed in.
const authJS = `() => {
  if (document.querySelector('.cf-turnstile, iframe[src*="challenges.cloudflare.com"], #px-captcha, [id^="px-captcha"]')) return 'captcha';
  const p = location.pathname;
  if (/account-security|\/login\b|\/signup/i.test(p)) return 'login';
  if (/^\/nx\//.test(p) && window.__NUXT__) return 'in';
  return 'unknown';
}`

// AuthState reports whether the current (auth-gated) page shows a signed-in session.
func AuthState(page *rod.Page) Auth {
	obj, err := page.Eval(authJS)
	if err != nil {
		return AuthUnknown
	}
	switch Auth(obj.Value.Str()) {
	case AuthIn:
		return AuthIn
	case AuthLogin:
		return AuthLogin
	case AuthCaptcha:
		return AuthCaptcha
	default:
		return AuthUnknown
	}
}

// ProfileDir returns the profile directory this browser was launched with, for
// reporting to the user.
func (b *Browser) ProfileDir() string { return b.profileDir }
