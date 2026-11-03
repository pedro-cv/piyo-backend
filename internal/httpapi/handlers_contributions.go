// Route contributions: a user sends the routes they created or updated in a
// local data provider (docs/implementation/CONTRIBUTIONS_DESIGN.md). Reviewed
// by a maintainer like suggestions, but with a *monthly* per-device quota
// (MAX_CONTRIBUTIONS_PER_MONTH) so reviewers have time to check each one, a
// bigger body cap (model.MaxContributionBody) and whole-route validation.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"piyo-backend/internal/dataset"
	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

// contributionLine / contributionVariant mirror the dataset package shapes
// (DATA_MODEL.md §3) the app already produces; only the fields validated here
// are declared, the payload is stored verbatim.
type contributionLine struct {
	LineID  string `json:"line_id"`
	Display string `json:"display"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Color   string `json:"color"`
}

type contributionVariant struct {
	VariantID string `json:"variant_id"`
	LineID    string `json:"line_id"`
	Direction string `json:"direction"`
	Headsign  string `json:"headsign"`
	Origin    string `json:"origin"`
	Polyline  string `json:"polyline"`
}

type contributionEdit struct {
	Op             string            `json:"op"` // add | update
	BasedOnVersion int               `json:"based_on_version"`
	Before         map[string]string `json:"before"` // variant_id -> original polyline
}

type createContributionRequest struct {
	ProviderName       string                      `json:"provider_name"`
	BaseDatasetVersion int                         `json:"base_dataset_version"`
	AppVersion         string                      `json:"app_version"`
	Comment            string                      `json:"comment"`
	Lines              []contributionLine          `json:"lines"`
	Variants           []contributionVariant       `json:"variants"`
	Edits              map[string]contributionEdit `json:"edits"`
}

// validate returns a user-facing message for the first problem found and the
// line ids in order.
func (in *createContributionRequest) validate(s *Server) (routeIDs []string, msg string) {
	in.ProviderName = strings.TrimSpace(in.ProviderName)
	in.AppVersion = strings.TrimSpace(in.AppVersion)
	in.Comment = strings.TrimSpace(in.Comment)
	switch {
	case in.AppVersion == "":
		return nil, "app_version is required"
	case utf8.RuneCountInString(in.AppVersion) > model.MaxAppVersion:
		return nil, "app_version too long"
	case utf8.RuneCountInString(in.ProviderName) > model.MaxProviderName:
		return nil, "provider_name too long"
	case utf8.RuneCountInString(in.Comment) > model.MaxReportComment:
		return nil, "comment must be at most 500 characters"
	case in.BaseDatasetVersion < 0:
		return nil, "base_dataset_version must be >= 0"
	case len(in.Lines) == 0:
		return nil, "at least one line is required"
	case len(in.Lines) > model.MaxContributionRoutes:
		return nil, "too many lines (max 10 per contribution)"
	}
	seen := map[string]bool{}
	for _, l := range in.Lines {
		id := strings.TrimSpace(l.LineID)
		switch {
		case id == "" || utf8.RuneCountInString(id) > model.MaxLineID:
			return nil, "every line needs a line_id (max 32 characters)"
		case seen[id]:
			return nil, "duplicate line_id " + id
		case strings.TrimSpace(l.Display) == "" || utf8.RuneCountInString(l.Display) > 12:
			return nil, "line " + id + ": display is required (max 12 characters)"
		case utf8.RuneCountInString(l.Name) > 80:
			return nil, "line " + id + ": name too long"
		case l.Color != "" && !isHexColor(l.Color):
			return nil, "line " + id + ": color must be #RRGGBB"
		}
		seen[id] = true
		routeIDs = append(routeIDs, id)
	}
	variantsPerLine := map[string]int{}
	for _, v := range in.Variants {
		if !seen[v.LineID] {
			return nil, "variant " + v.VariantID + " refers to a line not in this contribution"
		}
		if v.VariantID == "" || utf8.RuneCountInString(v.VariantID) > model.MaxVariantID {
			return nil, "every variant needs a variant_id (max 64 characters)"
		}
		if v.Direction != "ida" && v.Direction != "vuelta" && v.Direction != "variante" {
			return nil, "variant " + v.VariantID + ": direction must be ida, vuelta or variante"
		}
		if utf8.RuneCountInString(v.Headsign) > 80 || utf8.RuneCountInString(v.Origin) > 80 {
			return nil, "variant " + v.VariantID + ": headsign/origin too long"
		}
		pts := dataset.DecodePolyline(v.Polyline)
		if len(pts) < 2 {
			return nil, "variant " + v.VariantID + ": polyline needs at least 2 points"
		}
		if len(pts) > model.MaxContributionPoints {
			return nil, "variant " + v.VariantID + ": too many points (max 600)"
		}
		for _, p := range pts {
			if !s.cfg.MetroBounds.Contains(p.Lat, p.Lng) {
				return nil, "variant " + v.VariantID + ": every point must be inside the metro area " + s.cfg.MetroBounds.String()
			}
		}
		variantsPerLine[v.LineID]++
	}
	for _, id := range routeIDs {
		if variantsPerLine[id] == 0 {
			return nil, "line " + id + " has no variants"
		}
	}
	for id, e := range in.Edits {
		if !seen[id] {
			return nil, "edits." + id + " refers to a line not in this contribution"
		}
		if e.Op != "add" && e.Op != "update" {
			return nil, "edits." + id + ".op must be add or update"
		}
		for vid, before := range e.Before {
			if len(dataset.DecodePolyline(before)) < 2 {
				return nil, "edits." + id + ".before." + vid + " is not a polyline"
			}
		}
	}
	return routeIDs, ""
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// utcMonthStart returns 00:00 UTC on the first day of t's month.
func utcMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nextUTCMonth(t time.Time) time.Time { return utcMonthStart(t).AddDate(0, 1, 0) }

// handleContributionQuota: GET /v1/contributions/quota
func (s *Server) handleContributionQuota(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	block, err := s.store.GetBlock(r.Context(), hash)
	if err != nil {
		s.internalError(w, r, "contribution quota: get block", err)
		return
	}
	used, err := s.store.CountContributionsSince(r.Context(), hash, utcMonthStart(now))
	if err != nil {
		s.internalError(w, r, "contribution quota: count", err)
		return
	}
	var blockedUntil *time.Time
	if block != nil {
		blockedUntil = &block.Until
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limit":         s.cfg.MaxContributionsPerMonth,
		"remaining":     remaining(s.cfg.MaxContributionsPerMonth, used),
		"resets_at":     nextUTCMonth(now).Format(time.RFC3339),
		"blocked_until": rfc3339(blockedUntil),
		"max_routes":    model.MaxContributionRoutes,
	})
}

// handleCreateContribution: POST /v1/contributions
func (s *Server) handleCreateContribution(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	// Decode once into the typed request for validation; keep the raw bytes as the payload.
	var raw json.RawMessage
	if status, err := decodeJSON(r, &raw); err != nil {
		writeError(w, status, "invalid_json", err.Error(), nil)
		return
	}
	if len(raw) > model.MaxContributionPayload {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "contribution too large (max 448 KB)", nil)
		return
	}
	var in createContributionRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	routeIDs, msg := in.validate(s)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "validation_error", msg, nil)
		return
	}
	now := time.Now().UTC()
	ctx := r.Context()
	block, err := s.store.GetBlock(ctx, hash)
	if err != nil {
		s.internalError(w, r, "create contribution: get block", err)
		return
	}
	if block != nil {
		writeError(w, http.StatusForbidden, "blocked", "this device is temporarily blocked from contributing",
			map[string]any{"blocked_until": block.Until.UTC().Format(time.RFC3339)})
		return
	}
	used, err := s.store.CountContributionsSince(ctx, hash, utcMonthStart(now))
	if err != nil {
		s.internalError(w, r, "create contribution: count", err)
		return
	}
	if used >= s.cfg.MaxContributionsPerMonth {
		writeError(w, http.StatusTooManyRequests, "quota_exceeded", "monthly contribution limit reached",
			map[string]any{"limit": s.cfg.MaxContributionsPerMonth, "resets_at": nextUTCMonth(now).Format(time.RFC3339)})
		return
	}
	// Store the payload without the envelope fields that live in their own columns.
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(raw, &payload)
	delete(payload, "provider_name")
	delete(payload, "app_version")
	delete(payload, "comment")
	stored, _ := json.Marshal(payload)
	c := &model.Contribution{
		ID: newID("c_"), DeviceHash: hash, ProviderName: in.ProviderName, BaseDatasetVersion: in.BaseDatasetVersion,
		RouteIDs: routeIDs, Payload: stored, Comment: in.Comment, AppVersion: in.AppVersion,
		Status: model.StatusPending, CreatedAt: now,
	}
	if err := s.store.CreateContribution(ctx, c); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			c.ID = newID("c_")
			err = s.store.CreateContribution(ctx, c)
		}
		if err != nil {
			s.internalError(w, r, "create contribution: insert", err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        c.ID,
		"remaining": remaining(s.cfg.MaxContributionsPerMonth, used+1),
		"resets_at": nextUTCMonth(now).Format(time.RFC3339),
	})
}

// handleMyContributions: GET /v1/contributions/mine — statuses for this device (no payloads).
func (s *Server) handleMyContributions(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	list, err := s.store.ListContributionsByDevice(r.Context(), hash, 50)
	if err != nil {
		s.internalError(w, r, "my contributions", err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"id": c.ID, "status": c.Status, "review_note": c.ReviewNote, "route_ids": c.RouteIDs,
			"provider_name": c.ProviderName, "created_at": c.CreatedAt.Format(time.RFC3339), "reviewed_at": rfc3339(c.ReviewedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "contributions": out})
}

// --- admin -------------------------------------------------------------------

// handleAdminListContributions: GET /v1/admin/contributions?status=pending&limit=100
func (s *Server) handleAdminListContributions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = model.StatusPending
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	list, err := s.store.ListContributions(r.Context(), status, limit)
	if err != nil {
		if errors.Is(err, store.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "validation_error", "status must be pending, accepted, rejected or all", nil)
			return
		}
		s.internalError(w, r, "list contributions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(list), "contributions": list})
}

// handleAdminGetContribution: GET /v1/admin/contributions/{id} — full payload for review.
func (s *Server) handleAdminGetContribution(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetContribution(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such contribution", nil)
			return
		}
		s.internalError(w, r, "get contribution", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contribution": c})
}

func (s *Server) reviewContribution(w http.ResponseWriter, r *http.Request, status string) {
	id := r.PathValue("id")
	var in reviewRequest
	if r.ContentLength > 0 {
		if st, err := decodeJSON(r, &in); err != nil {
			writeError(w, st, "invalid_json", err.Error(), nil)
			return
		}
	}
	if utf8.RuneCountInString(in.Note) > model.MaxReviewNote {
		writeError(w, http.StatusBadRequest, "validation_error", "note too long", nil)
		return
	}
	now := time.Now().UTC()
	c, err := s.store.SetContributionStatus(r.Context(), id, status, strings.TrimSpace(in.Note), now)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such contribution", nil)
			return
		}
		s.internalError(w, r, "review contribution", err)
		return
	}
	resp := map[string]any{"contribution": c.Summary()}
	s.notifyDevice(c.DeviceHash, map[string]string{"type": "contribution_" + status, "id": c.ID})
	if status == model.StatusRejected {
		until, err := s.maybeAutoBlock(r, c.DeviceHash, now)
		if err != nil {
			s.internalError(w, r, "review contribution: auto block", err)
			return
		}
		if until != nil {
			resp["blocked_until"] = until.UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminAcceptContribution(w http.ResponseWriter, r *http.Request) {
	s.reviewContribution(w, r, model.StatusAccepted)
}

func (s *Server) handleAdminRejectContribution(w http.ResponseWriter, r *http.Request) {
	s.reviewContribution(w, r, model.StatusRejected)
}
