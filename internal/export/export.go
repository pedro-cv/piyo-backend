// Package export turns stored feedback into files a maintainer can take
// elsewhere: NPS as CSV (exported monthly) and reports as JSON. It also
// defines IssueFiler, the hook through which accepted reports will later be
// turned into public GitHub issues.
//
// Only LogIssueFiler exists today; it logs what *would* be filed. A
// GitHubIssueFiler (POST /repos/{owner}/{repo}/issues with a token, body
// built from the report, label per report type) is planned for a next
// iteration and must plug in without touching the HTTP layer.
package export

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"piyo-backend/internal/model"
)

// NPSCSVHeader is the first row of the CSV export.
var NPSCSVHeader = []string{
	"id", "created_at", "score", "reasons", "comment",
	"app_version", "data_version", "device_class", "device_hash",
}

// ExportNPSCSV writes entries as RFC 4180 CSV. Reasons are joined with "|".
func ExportNPSCSV(w io.Writer, entries []model.NPS) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(NPSCSVHeader); err != nil {
		return fmt.Errorf("export: csv header: %w", err)
	}
	for _, n := range entries {
		row := []string{
			n.ID,
			n.CreatedAt.UTC().Format(time.RFC3339),
			strconv.Itoa(n.Score),
			strings.Join(n.Reasons, "|"),
			n.Comment,
			n.AppVersion,
			strconv.Itoa(n.DataVersion),
			n.DeviceClass,
			n.DeviceHash,
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("export: csv row %s: %w", n.ID, err)
		}
	}
	cw.Flush()
	return cw.Error()
}

// ReportsExport is the envelope written by ExportReportsJSON.
type ReportsExport struct {
	ExportedAt time.Time      `json:"exported_at"`
	Count      int            `json:"count"`
	Reports    []model.Report `json:"reports"`
}

// ExportReportsJSON writes reports as a pretty-printed JSON document.
func ExportReportsJSON(w io.Writer, reports []model.Report) error {
	if reports == nil {
		reports = []model.Report{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ReportsExport{
		ExportedAt: time.Now().UTC(),
		Count:      len(reports),
		Reports:    reports,
	}); err != nil {
		return fmt.Errorf("export: reports json: %w", err)
	}
	return nil
}

// IssueFiler files an accepted report somewhere maintainers triage it
// (e.g. a public GitHub issue). Implementations must be idempotent-friendly:
// the caller may retry on error.
type IssueFiler interface {
	// FileIssue files the report and returns an opaque reference to the
	// created issue (URL or number as string).
	FileIssue(ctx context.Context, r model.Report) (ref string, err error)
}

// LogIssueFiler is the placeholder IssueFiler used today. It only logs the
// issue it would have created.
type LogIssueFiler struct {
	Log *slog.Logger
}

var _ IssueFiler = LogIssueFiler{}

// FileIssue logs the would-be issue and returns a synthetic reference.
func (f LogIssueFiler) FileIssue(_ context.Context, r model.Report) (string, error) {
	log := f.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("issue filer: would file issue",
		"report_id", r.ID,
		"title", IssueTitle(r),
		"type", r.Type,
		"line_id", r.LineID,
		"lat", r.Lat,
		"lng", r.Lng,
	)
	return "log:" + r.ID, nil
}

// IssueTitle builds the title a GitHubIssueFiler would use.
func IssueTitle(r model.Report) string {
	label := map[string]string{
		model.ReportTypeStop:    "Parada incorrecta",
		model.ReportTypeRoute:   "Recorrido incorrecto",
		model.ReportTypeGone:    "La línea ya no pasa",
		model.ReportTypeMissing: "Falta línea o trufi",
		model.ReportTypeOther:   "Otro",
	}[r.Type]
	if label == "" {
		label = r.Type
	}
	if r.LineID != "" {
		return fmt.Sprintf("[%s] Línea %s (%s)", label, r.LineID, r.ID)
	}
	return fmt.Sprintf("[%s] (%s)", label, r.ID)
}
