# upwork-feed-fetcher

[![Buy Me A Coffee](https://img.shields.io/badge/Buy%20Me%20A%20Coffee-%E2%98%95-orange.svg?style=flat-square)](https://buymeacoffee.com/doonfrs)

A command-line tool that exports your Upwork job feeds to **JSON** (or CSV/XML),
so a script or an AI model can read them, rank them, and decide what's worth
bidding on, without you (or an AI agent) clicking through a browser every time.

## Why

Browsing Upwork through an AI agent is slow and expensive. Every search, scroll,
and click burns screenshots and tokens. This tool flips it around:

1. Run it on a schedule (say, hourly). It opens Upwork in the background, grabs
   your feeds, and writes a clean JSON file.
2. Your script or AI reads that JSON, filters/ranks the jobs, and picks the good
   ones.
3. You only open the browser when there's actually a job worth bidding on.

Fast, cheap, and easy to automate.

## What it exports

Every job comes out as structured data. The exact schema (JSON field names) is below.

**Job**

| field | type | notes |
|-------|------|-------|
| `id` | string | ciphertext id, e.g. `~021234…` |
| `uid` | string | numeric id |
| `recno` | string | Upwork record number |
| `url` | string | derived from `id` |
| `title` | string | |
| `description` | string | |
| `type` | string | `hourly` or `fixed` |
| `hourlyMin` / `hourlyMax` | number | hourly range (0 if fixed) |
| `fixedBudget` | number | fixed price (0 if hourly) |
| `weeklyBudget` | number | rarely set |
| `engagement` | string | e.g. `Less than 30 hrs/week` |
| `duration` | string | e.g. `More than 6 months` |
| `experienceLevel` | string | `Entry Level` / `Intermediate` / `Expert` |
| `freelancersToHire` | int | |
| `proposalsTier` | string | bucket, e.g. `5 to 10` |
| `totalApplicants` | int | exact count — **my-feed only** |
| `premium` / `applied` / `enterprise` | bool | |
| `jobStatus` | string | e.g. `Open` — **my-feed only** |
| `isLocal` | bool | **my-feed only** |
| `prefFreelancerLocation` | string[] | preferred countries — **my-feed only** |
| `prefFreelancerLocationMandatory` | bool | |
| `createdOn` / `publishedOn` | string | ISO 8601 |
| `renewedOn` | string | ISO 8601 — **my-feed only** |
| `connectPrice` | int | connects to apply |
| `position` | int | rank within the feed |
| `skills` | string[] | |
| `tags` | string[] | e.g. `firstJobPost`, `contractToHireSet` |
| `client` | object | see below |

**Client**

| field | type | notes |
|-------|------|-------|
| `paymentVerified` | bool | |
| `totalSpent` | number | USD across all contracts |
| `totalReviews` | int | |
| `rating` | number | 0–5 |
| `totalHires` | int | |
| `totalPostedJobs` | int | **my-feed only** |
| `country` | string | name or ISO-3 code (Upwork is inconsistent) |
| `city` | string | usually empty in feeds |
| `topClient` | bool | |
| `financialPrivacy` | bool | client hides spend/reviews |
| `lastRecruitingActivity` | string | ISO 8601; empty if none |
| `companyOrgUid` | string | stable client-org id — **my-feed only** |

> **Feeds use two shapes.** `most-recent` and `best` send a *lean* payload;
> `my-feed` sends a *richer* one. Fields marked **my-feed only** above are absent
> from `recent`/`best` exports and come out as `0` / `""` / `[]` there. In `all`
> mode, jobs are deduplicated keeping the `myfeed` copy first, so shared jobs keep
> the richer fields.

> **Client average hourly rate & total hours billed** are not in any feed
> payload — Upwork only ships them on the **single job page**. The default
> background browser can't open that page (Cloudflare blocks it), but
> [`--attach`](#6-bypass-cloudflare-for-search--single-jobs---attach) **can**, so
> these fields *are* exported when you fetch a single job with `--attach`.

It can export any of your Find Work feeds:

| command  | feed                                              |
|----------|---------------------------------------------------|
| `myfeed` | your personalized feed (from your saved searches) |
| `best`   | Best Matches                                      |
| `recent` | Most Recent                                       |
| `saved`  | Saved (bookmarked) jobs                           |

…or a single job from its URL.

> **Keyword search** sits behind the same Cloudflare bot challenge that blocks the
> default background browser, so the feeds above are the easy path (set up Saved
> Searches on Upwork — they power `myfeed`). To export search results directly,
> use [`--attach`](#6-bypass-cloudflare-for-search--single-jobs---attach) with a
> search URL; it runs through your own Chrome, which Cloudflare lets through.

## Requirements

- Go 1.23+
- Google Chrome

## Build

```sh
go build -o upwork-feed-fetcher ./cmd/upwork-feed-fetcher
```

On Windows:

```powershell
go build -o upwork-feed-fetcher.exe ./cmd/upwork-feed-fetcher
```

## Usage

### 1. Sign in once

```sh
upwork-feed-fetcher login
```

Opens a visible Chrome. Sign in to Upwork (and solve any CAPTCHA). Your session
is saved and reused on every later run; you won't sign in again unless it expires.

### 2. Export a feed

```sh
upwork-feed-fetcher myfeed     # your personalized feed
upwork-feed-fetcher best       # best matches
upwork-feed-fetcher recent     # most recent
upwork-feed-fetcher saved      # saved jobs
```

These run in the background (no window) and write a file like
`upwork-feed-20260603-2130.json`.

### 3. Export everything at once

```sh
upwork-feed-fetcher all
```

Sweeps **myfeed + best + recent** in one run, merges them, removes duplicates, and
writes a single `upwork-all-….json`. This is the one to schedule.

### 4. Load more jobs per feed

You get the first page by default. To pull more, click "Load More" automatically:

```sh
upwork-feed-fetcher all --pages 2      # ~2 pages per feed
upwork-feed-fetcher recent --pages 3   # 3 pages
```

(Best Matches is a fixed list with no "Load More", so `--pages` only adds jobs to
`myfeed` and `recent`.)

### 5. A single job

```sh
upwork-feed-fetcher "https://www.upwork.com/jobs/~021234567890abcdef"
```

A single job needs `--attach` (next section) — Cloudflare blocks the default
background browser on job pages. Attaching also unlocks the client's **average
hourly rate** and **total hours billed**, which only appear on the job page.

### 6. Bypass Cloudflare for search & single jobs (`--attach`)

The default mode launches its own Chrome. Upwork's **search** and **single‑job**
pages sit behind a strict Cloudflare bot check that detects an automated browser
and never lets it in — the "Just a moment…" challenge loops forever, even with
`--gui`. (Detection is at the automation layer: Chrome's `--enable-automation`
flag plus the DevTools "Runtime" signal the tool needs to read the page. The feeds
in section 2 are *not* affected.)

The fix is to **attach to your own Chrome** — a real browser you control, which
clears Cloudflare normally — instead of launching one. The tool connects over
Chrome's debug port and reads the page; nothing automated goes through the gate,
and your browser is never closed or driven through the challenge.

**Step 1 — start your own Chrome with a debug port and a dedicated profile.**
Chrome 136+ ignores the debug port on your *default* profile (anti cookie‑theft),
so you must point it at a separate `--user-data-dir`:

```powershell
# Windows
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222 --user-data-dir="$env:LOCALAPPDATA\upwork-feed-fetcher\attach-profile"
```

```sh
# macOS
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --remote-debugging-port=9222 --user-data-dir="$HOME/.upwork-attach-profile"

# Linux
google-chrome --remote-debugging-port=9222 --user-data-dir="$HOME/.upwork-attach-profile"
```

**Step 2 — sign in to Upwork once** in that window (solve the Cloudflare check
yourself). It's a dedicated profile, so this is a one‑time login; it persists and
is reused on every later `--attach` run.

**Step 3 — fetch with `--attach`:**

```sh
# search — pass any Upwork search URL
upwork-feed-fetcher --attach "https://www.upwork.com/nx/search/jobs/?q=flutter"

# a single job — now includes the client's avg hourly rate & total hours
upwork-feed-fetcher --attach "https://www.upwork.com/jobs/~021234567890abcdef"

# feeds work through your session too
upwork-feed-fetcher --attach recent
```

**Most reliable — `--current`.** If a tool‑initiated navigation still gets
challenged, open the page yourself in that Chrome window, then let the tool read
whatever tab is already open. This removes *all* automated navigation through
Cloudflare:

```sh
# navigate to the search/job in your Chrome window first, then:
upwork-feed-fetcher --attach --current
```

If no debug Chrome is running, `--attach` prints the exact command to start one.
Use `--attach-port` if you ran Chrome on a port other than `9222`.

### Choosing the output

```sh
upwork-feed-fetcher all --output jobs.json      # name the file
upwork-feed-fetcher all --format csv            # or csv / xml
upwork-feed-fetcher all --format json,csv,xml   # several at once
```

With no `--output`, it writes `upwork-<type>-<timestamp>.json` in the current
folder.

## Options

| flag | default | what it does |
|------|---------|--------------|
| `--output` / `--out` | auto | output file (or name prefix when multiple formats) |
| `--format` | `json` | `json`, `csv`, `xml`, `all`, or a comma list like `json,csv` |
| `--pages` | `1` | pages to load per feed (clicks "Load More" `pages−1` times) |
| `--attach` | off | attach to **your own** running Chrome (debug port) instead of launching one — the way to reach search & single‑job pages past Cloudflare (see section 6) |
| `--attach-port` | `9222` | debug endpoint for `--attach` (e.g. `9222`, `:9222`, `host:9222`, or a full `ws://…` URL) |
| `--current` | off | with `--attach`: read the Upwork tab you already have open instead of navigating (most reliable past Cloudflare) |
| `--gui` | off | show the browser window (handy to watch a run) |
| `--hold` | off | do the action, then keep the window open until Ctrl+C (manual poking) |
| `--timeout` | `90s` | how long to wait for a page to load |
| `--dry-run` | off | print the URL(s) it would visit and exit (doesn't open the browser) |
| `--raw` | off | dump the untouched client/job payload from the page as JSON and exit (diagnostic; needs a single feed/job, no export) |
| `--profile` | app data dir | where the saved login/profile is kept |
| `--chrome` | system Chrome | path to a specific Chrome binary |

## Example: hourly triage

```sh
upwork-feed-fetcher all --pages 2 --output jobs.json
# → hand jobs.json to Claude / Codex / your script to rank and decide,
#   then open Upwork only for the jobs worth bidding on.
```

## Tests

```sh
go test ./...
```

## Support

If this tool saves you time, here's how to help keep it going:

- ⭐ **Star the repo** on GitHub
- ☕ **Buy me a coffee**: [buymeacoffee.com/doonfrs](https://buymeacoffee.com/doonfrs)

Every star and coffee means a lot and helps maintain the project! 🚀

## Author

**Feras Abdalrahman**

- GitHub: [@doonfrs](https://github.com/doonfrs)
- LinkedIn: [in/doonfrs](https://www.linkedin.com/in/doonfrs/)

💼 **Available for freelance work.** Need a developer for your project? Reach out
at [doonfrs@gmail.com](mailto:doonfrs@gmail.com).

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file
for details.
