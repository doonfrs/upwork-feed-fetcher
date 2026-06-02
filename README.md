# upwork-bid-helper

Drives a real Chrome (via [go-rod](https://github.com/go-rod/rod)) to export Upwork
**feed**, **search**, or **single-job** pages to **JSON / CSV / XML**. It reads the
data the page already loaded (`window.__NUXT__` / the Nuxt3 `__NUXT_DATA__` payload),
so there is no fragile HTML scraping.

> Console-only tool. Upcoming: hidden/off-screen mode, network interception, and
> richer single-job client data.

## Build

```sh
go build -o upwork-bid-helper ./cmd/upwork-bid-helper
```

Requires Go 1.23+ and Google Chrome installed.

## Usage

```sh
# Sign in once (opens a visible Chrome; session is saved & reused):
upwork-bid-helper login

# Shortcuts (no full URL needed) — one per find-work tab:
upwork-bid-helper myfeed          # My Feed
upwork-bid-helper best            # Best Matches
upwork-bid-helper recent          # Most Recent
upwork-bid-helper saved           # Saved Jobs

# A full Upwork URL also works (feed, search, or a job page):
upwork-bid-helper "https://www.upwork.com/jobs/~02xxxxxxxxxxxxxxxxx"

# Or build a search from key=value args (bare words become the query):
upwork-bid-helper q="react native" payment_verified=1
```

Available shortcuts: `myfeed`, `best`, `recent`, `saved`.

**Visibility:** `login` opens a **visible** window so you can sign in (and solve any
CAPTCHA); the session is saved to a persistent profile and reused on later runs.
Exports (`recent`, a URL, a search) run **headless** (background, no window) — with the
restored session cookies Upwork serves the authenticated pages fine. If an export hits a
login/challenge wall it exits with `login required — run: upwork-bid-helper login`.

### Flags

| flag | default | meaning |
|------|---------|---------|
| `--format` | `json` | `json` \| `csv` \| `xml` \| `all` (or comma-separated, e.g. `json,csv`) |
| `--out` | auto | output file, or filename prefix when multiple formats |
| `--chrome` | system Chrome | path to a Chrome binary |
| `--profile` | app config dir | persistent profile directory |
| `--timeout` | `90s` | max wait for the page to load |
| `--dry-run` | off | print the resolved target URL and exit (does not open the browser) |

`file://` paths are accepted as targets so you can export from a saved page offline.

## Test

```sh
go test ./...
```

The extractor tests load the saved samples in `temp/` (a local, gitignored scratch
dir) in headless Chrome and assert the feed/search/single-job extraction.

## Layout

- `cmd/upwork-bid-helper` — CLI entrypoint
- `internal/browser` — launch, persistent profile, challenge detection, teardown
- `internal/extract` — page-type detection + `window.__NUXT__`/devalue extractor (`extract.js`)
- `internal/model` — normalized `Job` / `Client` / `Result`
- `internal/export` — JSON / CSV (formula-injection guarded) / XML (escaped)
- `internal/search` — `key=value` → search URL builder
