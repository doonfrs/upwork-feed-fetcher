package extract_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/doonfrs/upwork-feed-fetcher/internal/browser"
	"github.com/doonfrs/upwork-feed-fetcher/internal/extract"
	"github.com/doonfrs/upwork-feed-fetcher/internal/model"
)

// loadSample loads a saved HTML sample from temp/ into a headless Chrome and
// returns the extraction result. Headless is fine here: file:// has no bot
// detection. Skips if Chrome or the sample is unavailable.
func loadSample(t *testing.T, name string) *model.Result {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "temp", name))
	if err != nil {
		t.Fatal(err)
	}
	b, err := browser.Launch(browser.Options{Headless: true, ProfileDir: t.TempDir()})
	if err != nil {
		t.Skipf("cannot launch chrome: %v", err)
	}
	t.Cleanup(b.Close)

	page, err := b.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	page = page.Timeout(40 * time.Second)
	if err := page.Navigate("file://" + abs); err != nil {
		t.Skipf("cannot open sample %s: %v", name, err)
	}
	_ = page.WaitLoad()
	waitReady(page)

	res, err := extract.Run(page)
	if err != nil {
		t.Fatalf("extract %s: %v", name, err)
	}
	return res
}

func waitReady(page *rod.Page) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if browser.Probe(page) == browser.StatusReady {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func TestExtractFeed(t *testing.T) {
	cases := map[string]int{
		"most-recent.html": 30,
		"my-feed.html":     30,
		"best-match.html":  30,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			res := loadSample(t, name)
			if res.PageType != model.PageFeed {
				t.Errorf("pageType = %q, want feed", res.PageType)
			}
			if res.Count != want || len(res.Jobs) != want {
				t.Fatalf("count = %d (jobs %d), want %d", res.Count, len(res.Jobs), want)
			}
			j := res.Jobs[0]
			if j.Title == "" || j.ID == "" || j.URL == "" {
				t.Errorf("first job missing core fields: %+v", j)
			}
			if !strings.Contains(j.URL, "upwork.com/jobs/") {
				t.Errorf("job URL not derived: %q", j.URL)
			}
		})
	}
}

func TestExtractSearch(t *testing.T) {
	res := loadSample(t, "search.html")
	if res.PageType != model.PageSearch {
		t.Errorf("pageType = %q, want search", res.PageType)
	}
	if res.Count == 0 {
		t.Fatalf("no jobs extracted from search sample")
	}
	if res.Jobs[0].Title == "" {
		t.Errorf("first search job missing title")
	}
}

func TestExtractSingleJob(t *testing.T) {
	// Offline, the single-job page does not hydrate window.__NUXT__.state, so
	// this exercises the devalue decoder against <script id="__NUXT_DATA__">.
	res := loadSample(t, "single-job.html")
	if res.PageType != model.PageJob {
		t.Errorf("pageType = %q, want job", res.PageType)
	}
	if res.Job == nil {
		t.Fatalf("single job not extracted (devalue decode failed); error=%q", res.Error)
	}
	if res.Job.Title == "" || res.Job.ID == "" {
		t.Errorf("single job missing core fields: %+v", res.Job)
	}
}
