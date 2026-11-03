// Push notifications: the app registers its FCM token for the device hash it
// already sends, and the API pushes a data-only message when an admin
// reviews that device's report or contribution, or announces a dataset
// release to the "dataset" topic. Push is a best effort: a failed send never
// fails the request that caused it. Tokens are never logged.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"piyo-backend/internal/push"
)

// TopicDataset is the FCM topic the app subscribes to for new releases.
const TopicDataset = "dataset"

// PushTokenMaxAge: tokens not refreshed for this long are purged (the app
// refreshes weekly while notifications are on).
const PushTokenMaxAge = 60 * 24 * time.Hour

// FCM registration tokens are ~150-200 chars of [A-Za-z0-9_:-].
var pushTokenRe = regexp.MustCompile(`^[A-Za-z0-9_:\-]+$`)

func validPushToken(t string) bool {
	return len(t) >= 32 && len(t) <= 4096 && pushTokenRe.MatchString(t)
}

type pushTokenRequest struct {
	Token string `json:"token"`
}

// handleSetPushToken: PUT /v1/push/token. One row per device (upsert), so
// the table cannot grow per request; the per-IP limiter caps the rate.
func (s *Server) handleSetPushToken(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	var in pushTokenRequest
	if st, err := decodeJSON(r, &in); err != nil {
		writeError(w, st, "invalid_json", err.Error(), nil)
		return
	}
	if !validPushToken(in.Token) {
		writeError(w, http.StatusBadRequest, "validation_error", "token must be an FCM registration token", nil)
		return
	}
	if err := s.store.SetPushToken(r.Context(), hash, in.Token, time.Now().UTC()); err != nil {
		s.internalError(w, r, "set push token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePushToken: DELETE /v1/push/token (notifications turned off).
func (s *Server) handleDeletePushToken(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireDevice(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePushToken(r.Context(), hash); err != nil {
		s.internalError(w, r, "delete push token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyDevice sends a data message to the device's token, if it has one,
// and deletes the token when FCM says it is unregistered. It runs inside the
// admin request on purpose: Cloud Run throttles CPU once the response is
// written, so a goroutine could stall until the next request. Errors are
// logged, never returned.
func (s *Server) notifyDevice(deviceHash string, data map[string]string) {
	if !s.push.Enabled() || deviceHash == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	tok, err := s.store.GetPushToken(ctx, deviceHash)
	if err != nil || tok == "" {
		if err != nil {
			s.log.Warn("push: read token", "err", err)
		}
		return
	}
	err = s.push.Send(ctx, push.Message{Token: tok, Data: data, High: true})
	switch {
	case errors.Is(err, push.ErrUnregistered):
		if err := s.store.DeletePushToken(ctx, deviceHash); err != nil {
			s.log.Warn("push: delete stale token", "err", err)
		}
	case err != nil:
		s.log.Warn("push: send", "type", data["type"], "err", err)
	}
}

type pushDatasetRequest struct {
	Version int `json:"version"`
}

// handleAdminPushDataset: POST /v1/admin/push/dataset {"version":N}
// announces release N (default: latest on disk) to the dataset topic. The
// app shows a notification only when N is newer than its local data.
func (s *Server) handleAdminPushDataset(w http.ResponseWriter, r *http.Request) {
	if !s.push.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "push_disabled", "set FCM_PROJECT_ID to enable push", nil)
		return
	}
	var in pushDatasetRequest
	if r.ContentLength > 0 {
		if st, err := decodeJSON(r, &in); err != nil {
			writeError(w, st, "invalid_json", err.Error(), nil)
			return
		}
	}
	if in.Version <= 0 && s.catalog != nil {
		if m, err := s.catalog.Latest(); err == nil {
			in.Version = m.DatasetVersion
		}
	}
	if in.Version <= 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "version required: no release on disk", nil)
		return
	}
	msg := push.Message{Topic: TopicDataset, Data: map[string]string{"type": "dataset", "version": strconv.Itoa(in.Version)}}
	if err := s.push.Send(r.Context(), msg); err != nil {
		s.log.Warn("push: dataset topic", "version", in.Version, "err", err)
		writeError(w, http.StatusBadGateway, "push_failed", "FCM did not accept the message", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "topic": TopicDataset, "version": in.Version})
}
