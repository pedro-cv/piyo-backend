// Maintainer endpoints, protected by X-Admin-Token (constant-time compare
// against ADMIN_TOKEN): review reports, manage blocks and export data.
//
// Auto-block rule: when a reject brings the device to REJECTS_TO_BLOCK
// rejected reports within the last REJECT_WINDOW_DAYS, the device is blocked
// for BLOCK_DAYS and the response carries "blocked_until".
package httpapi

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"piyo-backend/internal/export"
	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

// HeaderAdminToken carries the maintainer secret.
const HeaderAdminToken = "X-Admin-Token"

// requireAdmin wraps h with the admin token check.
func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	expected := []byte(s.cfg.AdminToken)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get(HeaderAdminToken))
		if len(expected) == 0 || subtle.ConstantTimeCompare(got, expected) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid "+HeaderAdminToken, nil)
			return
		}
		h(w, r)
	}
}

// handleAdminListReports: GET /v1/admin/reports?status=pending|accepted|rejected|all&limit=100
// Default status is "pending".
func (s *Server) handleAdminListReports(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = model.StatusPending
	}
	if status != store.StatusAll && !model.IsReportStatus(status) {
		writeError(w, http.StatusBadRequest, "validation_error",
			"status must be one of: pending, accepted, rejected, all", nil)
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	reports, err := s.store.ListReports(r.Context(), status, limit)
	if err != nil {
		s.internalError(w, r, "admin: list reports", err)
		return
	}
	if reports == nil {
		reports = []model.Report{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  status,
		"count":   len(reports),
		"reports": reports,
	})
}

// reviewedReport is a report plus the optional auto-block outcome.
type reviewedReport struct {
	model.Report
	BlockedUntil *string `json:"blocked_until,omitempty"`
}

// handleAdminAcceptReport: POST /v1/admin/reports/{id}/accept
func (s *Server) handleAdminAcceptReport(w http.ResponseWriter, r *http.Request) {
	s.reviewReport(w, r, model.StatusAccepted)
}

// handleAdminRejectReport: POST /v1/admin/reports/{id}/reject
func (s *Server) handleAdminRejectReport(w http.ResponseWriter, r *http.Request) {
	s.reviewReport(w, r, model.StatusRejected)
}

// reviewReport sets the status and, for rejections, applies the auto-block
// rule.
func (s *Server) reviewReport(w http.ResponseWriter, r *http.Request, status string) {
	id := r.PathValue("id")
	ctx := r.Context()
	now := time.Now().UTC()

	rep, err := s.store.SetReportStatus(ctx, id, status, now)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "report not found", nil)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin: set report status", err)
		return
	}
	out := reviewedReport{Report: *rep}

	if status == model.StatusAccepted {
		// Only acceptances are pushed: a rejected report has nothing to show the user.
		s.notifyDevice(rep.DeviceHash, map[string]string{"type": "report_accepted", "id": rep.ID})
		// Next iteration: a GitHubIssueFiler. Today this only logs.
		filer := export.LogIssueFiler{Log: s.log}
		if _, err := filer.FileIssue(ctx, *rep); err != nil {
			s.log.Warn("issue filer failed", "report_id", rep.ID, "err", err)
		}
	}

	if status == model.StatusRejected {
		until, err := s.maybeAutoBlock(r, rep.DeviceHash, now)
		if err != nil {
			s.internalError(w, r, "admin: auto block", err)
			return
		}
		if until != nil {
			str := until.Format(time.RFC3339)
			out.BlockedUntil = &str
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// maybeAutoBlock blocks the device when its recent rejections reach the
// threshold. It returns the block expiry when a block is active.
func (s *Server) maybeAutoBlock(r *http.Request, deviceHash string, now time.Time) (*time.Time, error) {
	ctx := r.Context()
	since := now.AddDate(0, 0, -s.cfg.RejectWindowDays)
	rejected, err := s.store.CountRejectedSince(ctx, deviceHash, since)
	if err != nil {
		return nil, err
	}
	rejectedSugg, err := s.store.CountRejectedSuggestionsSince(ctx, deviceHash, since)
	if err != nil {
		return nil, err
	}
	rejected += rejectedSugg
	rejectedContrib, err := s.store.CountRejectedContributionsSince(ctx, deviceHash, since)
	if err != nil {
		return nil, err
	}
	rejected += rejectedContrib
	if rejected < s.cfg.RejectsToBlock {
		return nil, nil
	}
	// Do not shorten an existing longer block.
	if existing, err := s.store.GetBlock(ctx, deviceHash); err != nil {
		return nil, err
	} else if existing != nil {
		return &existing.Until, nil
	}
	b := model.Block{
		DeviceHash: deviceHash,
		Until:      now.AddDate(0, 0, s.cfg.BlockDays),
		Reason:     fmt.Sprintf("%d rejected reports or suggestions", s.cfg.RejectsToBlock),
		CreatedAt:  now,
	}
	if err := s.store.SetBlock(ctx, b); err != nil {
		return nil, err
	}
	s.log.Info("device auto-blocked", "request_id", requestIDFrom(ctx), "until", b.Until.Format(time.RFC3339))
	return &b.Until, nil
}

// handleAdminListBlocks: GET /v1/admin/blocks
func (s *Server) handleAdminListBlocks(w http.ResponseWriter, r *http.Request) {
	blocks, err := s.store.ListBlocks(r.Context())
	if err != nil {
		s.internalError(w, r, "admin: list blocks", err)
		return
	}
	if blocks == nil {
		blocks = []model.Block{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(blocks), "blocks": blocks})
}

type createBlockRequest struct {
	InstallID  string `json:"install_id"`
	DeviceHash string `json:"device_hash"`
	Days       int    `json:"days"`
	Reason     string `json:"reason"`
}

// handleAdminCreateBlock: POST /v1/admin/blocks
// Accepts {"install_id":...} (hashed server-side) or {"device_hash":...}.
func (s *Server) handleAdminCreateBlock(w http.ResponseWriter, r *http.Request) {
	var in createBlockRequest
	if status, err := decodeJSON(r, &in); err != nil {
		writeError(w, status, "invalid_json", err.Error(), nil)
		return
	}
	in.InstallID = strings.TrimSpace(in.InstallID)
	in.DeviceHash = strings.ToLower(strings.TrimSpace(in.DeviceHash))
	in.Reason = strings.TrimSpace(in.Reason)

	var hash string
	switch {
	case in.InstallID != "" && in.DeviceHash != "":
		writeError(w, http.StatusBadRequest, "validation_error", "send either install_id or device_hash, not both", nil)
		return
	case in.InstallID != "":
		if !installIDRe.MatchString(in.InstallID) {
			writeError(w, http.StatusBadRequest, "validation_error", "install_id must be 8-64 chars of [A-Za-z0-9-]", nil)
			return
		}
		hash = HashInstallID(s.cfg.HashSalt, in.InstallID)
	case in.DeviceHash != "":
		if !IsDeviceHash(in.DeviceHash) {
			writeError(w, http.StatusBadRequest, "validation_error", "device_hash must be 64 hex chars", nil)
			return
		}
		hash = in.DeviceHash
	default:
		writeError(w, http.StatusBadRequest, "validation_error", "install_id or device_hash is required", nil)
		return
	}
	if in.Days == 0 {
		in.Days = s.cfg.BlockDays
	}
	if in.Days < 1 || in.Days > 3650 {
		writeError(w, http.StatusBadRequest, "validation_error", "days must be between 1 and 3650", nil)
		return
	}
	if utf8.RuneCountInString(in.Reason) > 200 {
		writeError(w, http.StatusBadRequest, "validation_error", "reason must be at most 200 characters", nil)
		return
	}
	if in.Reason == "" {
		in.Reason = "manual block"
	}

	now := time.Now().UTC()
	b := model.Block{
		DeviceHash: hash,
		Until:      now.AddDate(0, 0, in.Days),
		Reason:     in.Reason,
		CreatedAt:  now,
	}
	if err := s.store.SetBlock(r.Context(), b); err != nil {
		s.internalError(w, r, "admin: set block", err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

// handleAdminDeleteBlock: DELETE /v1/admin/blocks/{device_hash}
func (s *Server) handleAdminDeleteBlock(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("device_hash"))
	if !IsDeviceHash(hash) {
		writeError(w, http.StatusBadRequest, "validation_error", "device_hash must be 64 hex chars", nil)
		return
	}
	err := s.store.DeleteBlock(r.Context(), hash)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no block for that device", nil)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin: delete block", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminExportNPSCSV: GET /v1/admin/nps.csv
func (s *Server) handleAdminExportNPSCSV(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListNPS(r.Context(), 1000)
	if err != nil {
		s.internalError(w, r, "admin: list nps", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="piyo-nps-%s.csv"`, time.Now().UTC().Format("2006-01")))
	w.WriteHeader(http.StatusOK)
	if err := export.ExportNPSCSV(w, entries); err != nil {
		s.log.Error("admin: nps csv write", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}

// handleAdminExportReportsJSON: GET /v1/admin/reports.json
func (s *Server) handleAdminExportReportsJSON(w http.ResponseWriter, r *http.Request) {
	reports, err := s.store.ListReports(r.Context(), store.StatusAll, 1000)
	if err != nil {
		s.internalError(w, r, "admin: list reports", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="piyo-reports-%s.json"`, time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)
	if err := export.ExportReportsJSON(w, reports); err != nil {
		s.log.Error("admin: reports json write", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}

// parseLimit parses a ?limit= value with a default and a 1..1000 range.
func parseLimit(raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 1000 {
		return 0, errors.New("limit must be an integer between 1 and 1000")
	}
	return n, nil
}
