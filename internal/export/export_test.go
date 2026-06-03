package export

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/doonfrs/upwork-feed-fetcher/internal/model"
)

func sample() *model.Result {
	return &model.Result{
		PageType: model.PageFeed,
		Count:    1,
		Jobs: []model.Job{{
			ID:          "~0123",
			URL:         "https://www.upwork.com/jobs/~0123",
			Title:       "=cmd|' /C calc'!A1", // formula-injection attempt
			Description: "needs <html> & \"quotes\"",
			Type:        "hourly",
			HourlyMin:   30,
			HourlyMax:   60,
			Skills:      []string{"Go", "React"},
			Client:      model.Client{PaymentVerified: true, TotalSpent: 1234.5, Rating: 4.9},
		}},
	}
}

func TestCSVInjectionGuard(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sample(), CSV); err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(&buf)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want header + 1 row, got %d rows", len(rows))
	}
	title := rows[1][2] // "title" is the 3rd column
	if !strings.HasPrefix(title, "'=") {
		t.Errorf("formula not neutralized, title=%q", title)
	}
}

func TestXMLEscaping(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sample(), XML); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "<html>") {
		t.Errorf("raw < not escaped in XML output")
	}
	if !strings.Contains(out, "&lt;html&gt;") || !strings.Contains(out, "&amp;") {
		t.Errorf("expected escaped entities in XML output:\n%s", out)
	}
}

func TestJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sample(), JSON); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`"pageType": "feed"`, `"count": 1`, `"id": "~0123"`, `"paymentVerified": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON missing %q in:\n%s", want, out)
		}
	}
	// Description must NOT be HTML-escaped (SetEscapeHTML(false)).
	if !strings.Contains(out, `<html>`) {
		t.Errorf("JSON unexpectedly HTML-escaped the description")
	}
}
