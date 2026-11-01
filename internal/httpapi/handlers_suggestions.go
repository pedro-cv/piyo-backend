// Device endpoints for data suggestions: proposed changes to lines, stops or
// geometry, reviewed later by a maintainer. Same protections as reports:
// per-device daily quota (MAX_SUGGESTIONS_PER_DAY), blocks shared with
// reports, payload size and point-count caps, coordinates inside the metro.
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

type createSuggestionRequest struct {
	Kind           string          `json:"kind"`
	LineID         string          `json:"line_id"`
	VariantID      string          `json:"variant_id"`
	DatasetVersion int             `json:"dataset_version"`
	Lat            *float64        `json:"lat"`
	Lng            *float64        `json:"lng"`
	Payload        json.RawMessage `json:"payload"`
	Comment        string          `json:"comment"`
	AppVersion     string          `json:"app_version"`
}

// validate returns a user-facing message for the first problem found.
func (in *createSuggestionRequest) validate(s *Server) string {
	in.Kind = strings.TrimSpace(in.Kind)
	in.LineID = strings.TrimSpace(in.LineID)
	in.VariantID = strings.TrimSpace(in.VariantID)
	in.Comment = strings.TrimSpace(in.Comment)
	in.AppVersion = strings.TrimSpace(in.AppVersion)
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage("{}")
	}
	switch {
	case !model.IsSuggestionKind(in.Kind):
		return "kind must be one of: " + strings.Join(model.SuggestionKinds, ", ")
	case in.Lat == nil || in.Lng == nil:
		return "lat and lng are required"
	case !s.cfg.MetroBounds.Contains(*in.Lat, *in.Lng):
		return "lat/lng must be inside the metro area " + s.cfg.MetroBounds.String()
	case utf8.RuneCountInString(in.LineID) > model.MaxLineID:
		return "line_id too long"
	case utf8.RuneCountInString(in.VariantID) > model.MaxVariantID:
		return "variant_id too long"
	case utf8.RuneCountInString(in.Comment) > model.MaxReportComment:
		return "comment must be at most 500 characters"
	case in.AppVersion == "":
		return "app_version is required"
	case utf8.RuneCountInString(in.AppVersion) > model.MaxAppVersion:
		return "app_version too long"
	case in.DatasetVersion < 0:
		return "dataset_version must be >= 0"
	case len(in.Payload) > model.MaxSuggestionPayload:
		return "payload too large (max 16 KB)"
	case !json.Valid(in.Payload) || !strings.HasPrefix(strings.TrimSpace(string(in.Payload)), "{"):
		return "payload must be a JSON object"
	}
	// Kind-specific checks on the payload.
	var p struct {
		Polyline string      `json:"polyline"`
		Points   [][]float64 `json:"points"`
	}
	_ = json.Unmarshal(in.Payload, &p)
	n := len(p.Points)
	if p.Polyline != "" {
		n = len(dataset.DecodePolyline(p.Polyline))
	}
	if n > model.MaxSuggestionPoints {
		return "payload has too many points (max 400)"
	}
	for _, pt := range p.Points {
		if len(pt) != 2 || !s.cfg.MetroBounds.Contains(pt[0], pt[1]) {
			return "payload.points must be [lat,lng] pairs inside the metro area"
		}
	}
	if (in.Kind == model.SuggestionPolyline || in.Kind == model.SuggestionMissingLine) && n < 2 {
		return "this kind needs a payload with at least 2 points (polyline or points)"
	}
	return ""
}

// handleSuggestionQuota: GET /v1/suggestions/quota
func (s *Server) handleSuggestionQuota(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	block, err := s.store.GetBlock(r.Context(), hash)
	if err != nil {
		s.internalError(w, r, "suggestion quota: get block", err)
		return
	}
	used, err := s.store.CountSuggestionsSince(r.Context(), hash, utcDayStart(now))
	if err != nil {
		s.internalError(w, r, "suggestion quota: count", err)
		return
	}
	var blockedUntil *time.Time
	if block != nil {
		blockedUntil = &block.Until
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limit":         s.cfg.MaxSuggestionsPerDay,
		"remaining":     remaining(s.cfg.MaxSuggestionsPerDay, used),
		"resets_at":     nextUTCMidnight(now).Format(time.RFC3339),
		"blocked_until": rfc3339(blockedUntil),
	})
}

// handleCreateSuggestion: POST /v1/suggestions
func (s *Server) handleCreateSuggestion(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	var in createSuggestionRequest
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
	block, err := s.store.GetBlock(ctx, hash)
	if err != nil {
		s.internalError(w, r, "create suggestion: get block", err)
		return
	}
	if block != nil {
		writeError(w, http.StatusForbidden, "blocked", "this device is temporarily blocked from sending suggestions",
			map[string]any{"blocked_until": block.Until.UTC().Format(time.RFC3339)})
		return
	}
	used, err := s.store.CountSuggestionsSince(ctx, hash, utcDayStart(now))
	if err != nil {
		s.internalError(w, r, "create suggestion: count", err)
		return
	}
	if used >= s.cfg.MaxSuggestionsPerDay {
		writeError(w, http.StatusTooManyRequests, "quota_exceeded", "daily suggestion limit reached",
			map[string]any{"limit": s.cfg.MaxSuggestionsPerDay, "resets_at": nextUTCMidnight(now).Format(time.RFC3339)})
		return
	}
	sg := &model.Suggestion{
		ID: newID("s_"), DeviceHash: hash, Kind: in.Kind, LineID: in.LineID, VariantID: in.VariantID,
		DatasetVersion: in.DatasetVersion, Lat: *in.Lat, Lng: *in.Lng, Payload: in.Payload, Comment: in.Comment,
		AppVersion: in.AppVersion, Status: model.StatusPending, CreatedAt: now,
	}
	if err := s.store.CreateSuggestion(ctx, sg); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			sg.ID = newID("s_")
			err = s.store.CreateSuggestion(ctx, sg)
		}
		if err != nil {
			s.internalError(w, r, "create suggestion: insert", err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        sg.ID,
		"remaining": remaining(s.cfg.MaxSuggestionsPerDay, used+1),
		"resets_at": nextUTCMidnight(now).Format(time.RFC3339),
	})
}

// --- admin -------------------------------------------------------------------

// handleAdminListSuggestions: GET /v1/admin/suggestions?status=pending&line_id=76&limit=100
func (s *Server) handleAdminListSuggestions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = model.StatusPending
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	list, err := s.store.ListSuggestions(r.Context(), status, strings.TrimSpace(r.URL.Query().Get("line_id")), limit)
	if err != nil {
		if errors.Is(err, store.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "validation_error", "status must be pending, accepted, rejected or all", nil)
			return
		}
		s.internalError(w, r, "list suggestions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(list), "suggestions": list})
}

type reviewRequest struct {
	Note string `json:"note"`
}

// handleAdminReviewSuggestion handles accept/reject. Rejections count
// towards the same auto-block as reports (REJECTS_TO_BLOCK within
// REJECT_WINDOW_DAYS, reports + suggestions combined).
func (s *Server) reviewSuggestion(w http.ResponseWriter, r *http.Request, status string) {
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
	sg, err := s.store.SetSuggestionStatus(r.Context(), id, status, strings.TrimSpace(in.Note), now)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such suggestion", nil)
			return
		}
		s.internalError(w, r, "review suggestion", err)
		return
	}
	resp := map[string]any{"suggestion": sg}
	if status == model.StatusRejected {
		until, err := s.maybeAutoBlock(r, sg.DeviceHash, now)
		if err != nil {
			s.internalError(w, r, "review suggestion: auto block", err)
			return
		}
		if until != nil {
			resp["blocked_until"] = until.UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminAcceptSuggestion(w http.ResponseWriter, r *http.Request) {
	s.reviewSuggestion(w, r, model.StatusAccepted)
}

func (s *Server) handleAdminRejectSuggestion(w http.ResponseWriter, r *http.Request) {
	s.reviewSuggestion(w, r, model.StatusRejected)
}

// handleAdminDatasets: GET /v1/admin/datasets — versions on disk with their manifests.
func (s *Server) handleAdminDatasets(w http.ResponseWriter, r *http.Request) {
	out := []any{}
	if s.catalog != nil {
		for _, v := range s.catalog.Versions() {
			if m, err := s.catalog.Manifest(v); err == nil {
				out = append(out, m)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "datasets": out, "releases_dir": s.cfg.ReleasesDir})
}
