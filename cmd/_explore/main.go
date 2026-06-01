// Temporary exploration tool: load a saved Upwork HTML file in real Chrome,
// then probe window.__NUXT__ to discover where job records live and what fields exist.
// Removed once the extractor is written.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

const probeJS = `() => {
  const out = { nuxtType: typeof window.__NUXT__, findings: [] };
  const seen = new Set();
  const looksJob = (o) => o && typeof o === 'object' && 'ciphertext' in o && ('title' in o || 'description' in o);
  function walk(node, path, depth) {
    if (depth > 8 || node === null || typeof node !== 'object') return;
    if (seen.has(node)) return; seen.add(node);
    if (Array.isArray(node)) {
      if (node.length && looksJob(node[0])) {
        out.findings.push({ path, count: node.length, sampleKeys: Object.keys(node[0]) });
      }
      for (let i = 0; i < Math.min(node.length, 3); i++) walk(node[i], path + '[' + i + ']', depth + 1);
    } else {
      for (const k of Object.keys(node)) walk(node[k], path + '.' + k, depth + 1);
    }
  }
  try { walk(window.__NUXT__, '__NUXT__', 0); } catch (e) { out.error = String(e); }
  return out;
}`

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: _explore <file.html>")
		os.Exit(1)
	}
	abs, _ := filepath.Abs(os.Args[1])
	url := "file://" + abs

	l := launcher.New().
		Bin("/usr/bin/google-chrome").
		Headless(true).
		Set("no-sandbox").
		Leakless(false)
	controlURL := l.MustLaunch()
	browser := rod.New().ControlURL(controlURL).MustConnect()
	defer browser.MustClose()

	page := browser.MustPage()
	page = page.Timeout(40 * time.Second)
	page.MustNavigate(url)

	// Wait until the inline IIFE has populated window.__NUXT__ (feed pages) or give up.
	page.MustWaitLoad()
	time.Sleep(1 * time.Second)

	res := page.MustEval(probeJS)
	fmt.Println(res.String())
}
