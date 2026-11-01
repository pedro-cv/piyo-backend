// Device endpoints for line reports: daily quota lookup and submission.
//
// Quota rules: at most MAX_REPORTS_PER_DAY reports per device per UTC
// calendar day (the window resets at 00:00 UTC, which is 20:00 in Santa
// Cruz; "resets_at" tells the app exactly when). Blocked devices get 403
// with the unblock date so the app can show it.
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

// handleReportQuota: GET /v1/reports/quota
func (s *Server) handleReportQuota(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()

	block, err := s.store.GetBlock(r.Context(), hash)
	if err != nil {
		s.internalError(w, r, "quota: get block", err)
		return
	}
	used, err := s.store.CountReportsSince(r.Context(), hash, utcDayStart(now))
	if err != nil {
		s.internalError(w, r, "quota: count reports", err)
		return
	}
	var blockedUntil *time.Time
	if block != nil {
		blockedUntil = &block.Until
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limit":         s.cfg.MaxReportsPerDay,
		"remaining":     remaining(s.cfg.MaxReportsPerDay, used),
		"resets_at":     nextUTCMidnight(now).Format(time.RFC3339),
		"blocked_until": rfc3339(blockedUntil),
	})
}

type createReportRequest struct {
	Type        string   `json:"type"`
	Lat         *float64 `json:"lat"`
	Lng         *float64 `json:"lng"`
	LineID      string   `json:"line_id"`
	Comment     string   `json:"comment"`
	AppVersion  string   `json:"app_version"`
	DataVersion int      `json:"data_version"`
}

// validate returns a user-facing message for the first problem found.
func (in *createReportRequest) validate(s *Server) string {
	in.Type = strings.TrimSpace(in.Type)
	in.LineID = strings.TrimSpace(in.LineID)
	in.Comment = strings.TrimSpace(in.Comment)
	in.AppVersion = strings.TrimSpace(in.AppVersion)

	switch {
	case !model.IsReportType(in.Type):
		return "type must be one of: " + strings.Join(model.ReportTypes, ", ")
	case in.Lat == nil || in.Lng == nil:
		return "lat and lng are required"
	case !s.cfg.MetroBounds.Contains(*in.Lat, *in.Lng):
		return "lat/lng must be inside the metro area " + s.cfg.MetroBounds.String()
	case utf8.RuneCountInString(in.LineID) > model.MaxLineID:
		return "line_id too long"
	case utf8.RuneCountInString(in.Comment) > model.MaxReportComment:
		return "comment must be at most 500 characters"
	case in.AppVersion == "":
		return "app_version is required"
	case utf8.RuneCountInString(in.AppVersion) > model.MaxAppVersion:
		return "app_version too long"
	case in.DataVersion < 0:
		return "data_version must be >= 0"
	}
	return ""
}

// handleCreateReport: POST /v1/reports
func (s *Server) handleCreateReport(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	var in createReportRequest
	if status, err := decodeJSON(r, &in); err != nil {
		writeError(w, status, "invalid_json", err.Error(), nil)
		return
	}
	if msg := in.validate(s); msg != "" {
		writeError(w, http.StatusBadRequest, "validation_error", msg, nil)
		return
	}
	now := time.Now().UTC()
	ctx := r.Context()

	// Blocked?
	block, err := s.store.GetBlock(ctx, hash)
	if err != nil {
		s.internalError(w, r, "create report: get block", err)
		return
	}
	if block != nil {
		writeError(w, http.StatusForbidden, "blocked",
			"this device is temporarily blocked from sending reports",
			map[string]any{"blocked_until": block.Until.UTC().Format(time.RFC3339)})
		return
	}

	// Daily quota (UTC calendar day).
	used, err := s.store.CountReportsSince(ctx, hash, utcDayStart(now))
	if err != nil {
		s.internalError(w, r, "create report: count", err)
		return
	}
	if used >= s.cfg.MaxReportsPerDay {
		writeError(w, http.StatusTooManyRequests, "quota_exceeded",
			"daily report limit reached",
			map[string]any{
				"limit":     s.cfg.MaxReportsPerDay,
				"resets_at": nextUTCMidnight(now).Format(time.RFC3339),
			})
		return
	}

	rep := &model.Report{
		ID:          newID("r_"),
		DeviceHash:  hash,
		LineID:      in.LineID,
		Type:        in.Type,
		Lat:         *in.Lat,
		Lng:         *in.Lng,
		Comment:     in.Comment,
		AppVersion:  in.AppVersion,
		DataVersion: in.DataVersion,
		Status:      model.StatusPending,
		CreatedAt:   now,
	}
	if err := s.store.CreateReport(ctx, rep); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			// 48-bit random collision: astronomically rare, retry once.
			rep.ID = newID("r_")
			err = s.store.CreateReport(ctx, rep)
		}
		if err != nil {
			s.internalError(w, r, "create report: insert", err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        rep.ID,
		"remaining": remaining(s.cfg.MaxReportsPerDay, used+1),
		"resets_at": nextUTCMidnight(now).Format(time.RFC3339),
	})
}

func remaining(limit, used int) int {
	if used >= limit {
		return 0
	}
	return limit - used
}
