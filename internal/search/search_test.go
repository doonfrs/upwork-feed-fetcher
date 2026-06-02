package search

import (
	"net/url"
	"strings"
	"testing"
)

func TestIsURL(t *testing.T) {
	for _, s := range []string{"https://www.upwork.com/x", "http://a"} {
		if !IsURL(s) {
			t.Errorf("IsURL(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"q=react", "react native"} {
		if IsURL(s) {
			t.Errorf("IsURL(%q) = true, want false", s)
		}
	}
}

func TestParseArgsAndBuild(t *testing.T) {
	args := ParseArgs([]string{"q=react native", "category=web", "extra bare term"})
	if args["q"] != "react native extra bare term" {
		t.Errorf("q = %q", args["q"])
	}
	if args["category"] != "web" {
		t.Errorf("category = %q", args["category"])
	}

	u := BuildURL(args)
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "www.upwork.com" || parsed.Path != "/nx/search/jobs/" {
		t.Errorf("bad base URL: %s", u)
	}
	if got := parsed.Query().Get("q"); got != "react native extra bare term" {
		t.Errorf("query q = %q", got)
	}
	if got := parsed.Query().Get("category"); got != "web" {
		t.Errorf("query category = %q", got)
	}
}

func TestBuildURLEmpty(t *testing.T) {
	if got := BuildURL(nil); got != BaseURL {
		t.Errorf("BuildURL(nil) = %q, want %q", got, BaseURL)
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"alias myfeed", []string{"myfeed"}, URLMyFeed},
		{"alias best", []string{"best"}, URLBestMatches},
		{"alias recent", []string{"recent"}, URLMostRecent},
		{"alias case-insensitive", []string{"Recent"}, URLMostRecent},
		{"alias saved", []string{"saved"}, URLSavedJobs},
		{"full url passthrough", []string{"https://www.upwork.com/jobs/~02abc"}, "https://www.upwork.com/jobs/~02abc"},
		{"file url passthrough", []string{"file:///tmp/x.html"}, "file:///tmp/x.html"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Resolve(c.args); got != c.want {
				t.Errorf("Resolve(%q) = %q, want %q", c.args, got, c.want)
			}
		})
	}

	// An unknown single word is a search term, not an alias.
	if got := Resolve([]string{"react"}); !strings.HasPrefix(got, BaseURL) {
		t.Errorf("Resolve([react]) = %q, want a search URL", got)
	}
}
