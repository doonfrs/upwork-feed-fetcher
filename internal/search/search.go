// Package search builds an Upwork job-search URL from key=value arguments.
package search

import (
	"net/url"
	"strings"
)

const findWork = "https://www.upwork.com/nx/find-work/"

// Known Upwork page URLs, exposed as constants for reuse. These mirror the
// find-work tabs: My Feed (the base route), Best Matches, Most Recent, Saved Jobs.
const (
	// BaseURL is the Upwork job search endpoint.
	BaseURL = "https://www.upwork.com/nx/search/jobs/"
	// URLMyFeed is the "My Feed" tab (the default find-work page).
	URLMyFeed = findWork
	// URLBestMatches is the "Best Matches" tab.
	URLBestMatches = findWork + "best-matches"
	// URLMostRecent is the "Most Recent" tab.
	URLMostRecent = findWork + "most-recent"
	// URLSavedJobs is the "Saved Jobs" tab.
	URLSavedJobs = findWork + "saved-jobs"
)

// aliases map short names to full Upwork find-work URLs, one per tab. Keys are
// matched case-insensitively after trimming.
var aliases = map[string]string{
	"myfeed": URLMyFeed,
	"best":   URLBestMatches,
	"recent": URLMostRecent,
	"saved":  URLSavedJobs,
}

// Alias resolves a shortcut name to a full URL.
func Alias(name string) (string, bool) {
	u, ok := aliases[strings.ToLower(strings.TrimSpace(name))]
	return u, ok
}

// Resolve turns CLI args into a target URL:
//   - a single full URL (http/https/file) is used as-is
//   - a single known shortcut (myfeed, best, recent, saved) expands to its URL
//   - anything else is treated as key=val search args / bare query terms
func Resolve(args []string) string {
	if len(args) == 1 {
		if IsURL(args[0]) {
			return args[0]
		}
		if u, ok := Alias(args[0]); ok {
			return u
		}
	}
	return BuildURL(ParseArgs(args))
}

// IsURL reports whether s looks like a full URL rather than a key=val arg.
// file:// is accepted so the tool can export from a saved HTML page offline.
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "file://")
}

// ParseArgs turns ["q=react native", "category=web"] into a map. Values may
// contain '='; only the first '=' splits. Args without '=' become a bare query
// term appended to q.
func ParseArgs(args []string) map[string]string {
	m := map[string]string{}
	var terms []string
	for _, a := range args {
		if i := strings.Index(a, "="); i >= 0 {
			k := strings.TrimSpace(a[:i])
			v := a[i+1:]
			if k != "" {
				m[k] = v
			}
		} else if a != "" {
			terms = append(terms, a)
		}
	}
	if len(terms) > 0 {
		joined := strings.Join(terms, " ")
		if existing, ok := m["q"]; ok && existing != "" {
			m["q"] = existing + " " + joined
		} else {
			m["q"] = joined
		}
	}
	return m
}

// BuildURL assembles the search URL from parsed args. Unknown keys are passed
// through as query parameters so the tool stays useful as Upwork adds filters.
func BuildURL(args map[string]string) string {
	q := url.Values{}
	for k, v := range args {
		q.Set(k, v)
	}
	if len(q) == 0 {
		return BaseURL
	}
	return BaseURL + "?" + q.Encode()
}
