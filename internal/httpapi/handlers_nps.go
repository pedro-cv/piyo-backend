// Device endpoint for NPS feedback.
//
// Design choice: blocked devices may still send NPS. A block exists to stop
// bogus map corrections from reaching maintainers; NPS is an opinion score
// that is only aggregated, so it is not abuse-prone and the signal from an
// unhappy (possibly wrongly blocked) user is worth keeping. There is also no
// daily quota on NPS; the app itself only asks once per version.
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

type createNPSRequest struct {
	Score       *int     `json:"score"`
	Reasons     []string `json:"reasons"`
	Comment     string   `json:"comment"`
	AppVersion  string   `json:"app_version"`
	DataVersion int      `json:"data_version"`
	DeviceClass string   `json:"device_class"`
}

// validate returns a user-facing message for the first problem found and
// normalises the reasons list (trimmed, de-duplicated, never nil).
func (in *createNPSRequest) validate() string {
	in.Comment = strings.TrimSpace(in.Comment)
	in.AppVersion = strings.TrimSpace(in.AppVersion)
	in.DeviceClass = strings.TrimSpace(in.DeviceClass)

	if in.Score == nil || *in.Score < 0 || *in.Score > 10 {
		return "score must be an integer between 0 and 10"
	}
	if len(in.Reasons) > len(model.NPSReasons) {
		return "too many reasons"
	}
	seen := make(map[string]bool, len(in.Reasons))
	clean := make([]string, 0, len(in.Reasons))
	for _, raw := range in.Reasons {
		reason := strings.TrimSpace(raw)
		if !model.IsNPSReason(reason) {
			return "reasons must be a subset of: " + strings.Join(model.NPSReasons, ", ")
		}
		if !seen[reason] {
			seen[reason] = true
			clean = append(clean, reason)
		}
	}
	in.Reasons = clean

	switch {
	case utf8.RuneCountInString(in.Comment) > model.MaxNPSComment:
		return "comment must be at most 140 characters"
	case in.AppVersion == "":
		return "app_version is required"
	case utf8.RuneCountInString(in.AppVersion) > model.MaxAppVersion:
		return "app_version too long"
	case in.DataVersion < 0:
		return "data_version must be >= 0"
	case utf8.RuneCountInString(in.DeviceClass) > model.MaxDeviceClass:
		return "device_class too long"
	}
	return ""
}

// handleCreateNPS: POST /v1/nps
func (s *Server) handleCreateNPS(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	var in createNPSRequest
	if status, err := decodeJSON(r, &in); err != nil {
		writeError(w, status, "invalid_json", err.Error(), nil)
		return
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, "validation_error", msg, nil)
		return
	}

	n := &model.NPS{
		ID:          newID("n_"),
		DeviceHash:  hash,
		Score:       *in.Score,
		Reasons:     in.Reasons,
		Comment:     in.Comment,
		AppVersion:  in.AppVersion,
		DataVersion: in.DataVersion,
		DeviceClass: in.DeviceClass,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateNPS(r.Context(), n); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			n.ID = newID("n_")
			err = s.store.CreateNPS(r.Context(), n)
		}
		if err != nil {
			s.internalError(w, r, "create nps: insert", err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": n.ID})
}
