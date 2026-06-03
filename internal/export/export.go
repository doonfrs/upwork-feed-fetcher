// Package export serializes a model.Result to JSON, CSV, or XML.
package export

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/doonfrs/upwork-feed-fetcher/internal/model"
)

// Format is an output format identifier.
type Format string

const (
	JSON Format = "json"
	CSV  Format = "csv"
	XML  Format = "xml"
)

// Ext returns the file extension for a format (no leading dot).
func (f Format) Ext() string { return string(f) }

// Write serializes res to w in the given format.
func Write(w io.Writer, res *model.Result, f Format) error {
	switch f {
	case JSON:
		return writeJSON(w, res)
	case CSV:
		return writeCSV(w, res)
	case XML:
		return writeXML(w, res)
	default:
		return fmt.Errorf("unknown format %q", f)
	}
}

func writeJSON(w io.Writer, res *model.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(res)
}

func writeXML(w io.Writer, res *model.Result) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	// encoding/xml escapes element/attribute text automatically.
	type result struct {
		XMLName  xml.Name    `xml:"result"`
		PageType string      `xml:"pageType,attr"`
		Count    int         `xml:"count,attr"`
		Jobs     []model.Job `xml:"jobs>job"`
	}
	jobs := res.Jobs
	if len(jobs) == 0 && res.Job != nil {
		jobs = []model.Job{*res.Job}
	}
	if err := enc.Encode(result{PageType: string(res.PageType), Count: len(jobs), Jobs: jobs}); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// csvHeader is the flattened column order for CSV output.
var csvHeader = []string{
	"id", "url", "title", "type",
	"hourlyMin", "hourlyMax", "fixedBudget", "weeklyBudget",
	"engagement", "duration", "experienceLevel", "freelancersToHire",
	"proposalsTier", "premium", "applied", "enterprise",
	"createdOn", "publishedOn", "renewedOn", "connectPrice",
	"skills", "description",
	"client.paymentVerified", "client.totalSpent", "client.totalReviews",
	"client.rating", "client.totalHires", "client.country", "client.city",
	"client.topClient", "client.financialPrivacy",
}

func writeCSV(w io.Writer, res *model.Result) error {
	jobs := res.Jobs
	if len(jobs) == 0 && res.Job != nil {
		jobs = []model.Job{*res.Job}
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, j := range jobs {
		row := []string{
			j.ID, j.URL, j.Title, j.Type,
			f(j.HourlyMin), f(j.HourlyMax), f(j.FixedBudget), f(j.WeeklyBudget),
			j.Engagement, j.Duration, j.ExperienceLevel, strconv.Itoa(j.FreelancersToHire),
			j.ProposalsTier, b(j.Premium), b(j.Applied), b(j.Enterprise),
			j.CreatedOn, j.PublishedOn, j.RenewedOn, strconv.Itoa(j.ConnectPrice),
			strings.Join(j.Skills, "; "), j.Description,
			b(j.Client.PaymentVerified), f(j.Client.TotalSpent), strconv.Itoa(j.Client.TotalReviews),
			f(j.Client.Rating), strconv.Itoa(j.Client.TotalHires), j.Client.Country, j.Client.City,
			b(j.Client.TopClient), b(j.Client.FinancialPrivacy),
		}
		for i, v := range row {
			row[i] = sanitizeCSV(v)
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func b(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// sanitizeCSV defuses CSV/formula-injection: a leading =, +, -, @ (or a tab/CR
// before them) can make spreadsheet apps execute the cell. Prefix with a quote.
func sanitizeCSV(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
