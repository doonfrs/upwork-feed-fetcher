// Package extract runs the in-page extractor against a live rod page and
// returns a normalized model.Result.
package extract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod"

	"github.com/doonfrs/upwork-feed-fetcher/internal/model"
)

//go:embed extract.js
var extractJS string

// Run evaluates the extractor in the page's main world and parses the result.
func Run(page *rod.Page) (*model.Result, error) {
	obj, err := page.Eval(extractJS)
	if err != nil {
		return nil, fmt.Errorf("evaluate extractor: %w", err)
	}
	raw := obj.Value.Str()
	if raw == "" {
		return nil, fmt.Errorf("extractor returned empty result")
	}
	var res model.Result
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil, fmt.Errorf("parse extractor result: %w", err)
	}
	if res.PageType == "" {
		res.PageType = model.PageUnknown
	}
	return &res, nil
}

// Classify returns the page type implied by a URL, without touching the page.
// Used for logging and quick checks; the authoritative classification happens
// in the extractor against the loaded document.
func Classify(rawURL string) model.PageType {
	u := strings.ToLower(rawURL)
	switch {
	case strings.Contains(u, "/nx/search/jobs") || strings.Contains(u, "/search/"):
		return model.PageSearch
	case strings.Contains(u, "/nx/find-work") || strings.Contains(u, "/ab/find-work") || strings.Contains(u, "feed"):
		return model.PageFeed
	case strings.Contains(u, "/jobs/~") || strings.Contains(u, "/nx/job-details") || strings.Contains(u, "/jobs/"):
		return model.PageJob
	default:
		return model.PageUnknown
	}
}
