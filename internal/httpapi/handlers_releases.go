// Publishing a dataset release built from contributions (piyo-release,
// docs/implementation/CONTRIBUTIONS_DESIGN.md §6). The release files are
// produced offline and deployed with the API (Cloud Run bakes releases/ into
// the image), so this endpoint does not build anything: it checks that this
// server really serves version N, marks the contributions listed in the
// release manifest as published, notifies each sender once, and announces the
// release to the dataset topic. It is idempotent: a second call marks and
// notifies nobody again.
package httpapi

import (
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"piyo-backend/internal/model"
	"piyo-backend/internal/push"
)

type publishDatasetRequest struct {
	// Notify (default true) pushes contribution_published to each sender.
	Notify *bool `json:"notify"`
	// Announce (default true) pushes {"type":"dataset"} to the dataset topic.
	Announce *bool `json:"announce"`
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// handleAdminPublishDataset: POST /v1/admin/datasets/{version}/publish
func (s *Server) handleAdminPublishDataset(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || v <= 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "version must be a positive number", nil)
		return
	}
	var in publishDatasetRequest
	if r.ContentLength > 0 {
		if st, err := decodeJSON(r, &in); err != nil {
			writeError(w, st, "invalid_json", err.Error(), nil)
			return
		}
	}
	if s.catalog == nil {
		writeError(w, http.StatusNotFound, "not_found", "this server has no RELEASES_DIR", nil)
		return
	}
	m, err := s.catalog.Manifest(v)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "release v"+strconv.Itoa(v)+" is not deployed on this server", nil)
		return
	}
	if latest, err := s.catalog.Latest(); err != nil || latest.DatasetVersion != v {
		writeError(w, http.StatusConflict, "not_latest", "only the latest release can be published (the manifest the app sees)", nil)
		return
	}
	ids := []string{}
	var changed []string
	if m.Changes != nil {
		ids = m.Changes.ContributionIDs
		changed = append(append(changed, m.Changes.LinesAdded...), m.Changes.LinesUpdated...)
	}
	ctx := r.Context()
	now := time.Now().UTC()
	marked, err := s.store.MarkContributionsPublished(ctx, ids, v, now)
	if err != nil {
		s.internalError(w, r, "publish dataset: mark contributions", err)
		return
	}
	markedIDs := make([]string, 0, len(marked))
	perDevice := map[string]int{}
	var devices []string
	for _, c := range marked {
		markedIDs = append(markedIDs, c.ID)
		if perDevice[c.DeviceHash] == 0 {
			devices = append(devices, c.DeviceHash)
		}
		perDevice[c.DeviceHash]++
	}
	// Listed in the release but not marked now: already published, not accepted or unknown here.
	skipped := []string{}
	for _, id := range ids {
		if !slices.Contains(markedIDs, id) {
			skipped = append(skipped, id)
		}
	}

	notified := 0
	if boolOr(in.Notify, true) && s.push.Enabled() {
		// One message per device, however many of its routes are in. Sent inside the request
		// (see notifyDevice), a few at a time so a big release stays within the write timeout.
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, d := range devices {
			wg.Add(1)
			sem <- struct{}{}
			go func(d string) {
				defer func() { <-sem; wg.Done() }()
				s.notifyDevice(d, map[string]string{"type": "contribution_published", "version": strconv.Itoa(v), "count": strconv.Itoa(perDevice[d])})
			}(d)
		}
		wg.Wait()
		notified = len(devices)
	}
	announced := false
	if boolOr(in.Announce, true) && s.push.Enabled() {
		msg := push.Message{Topic: TopicDataset, Data: map[string]string{"type": "dataset", "version": strconv.Itoa(v)}}
		if err := s.push.Send(ctx, msg); err != nil {
			s.log.Warn("push: dataset topic", "version", v, "err", err)
		} else {
			announced = true
		}
	}

	// Reports made against older data about lines this release changed: the
	// reviewer should check whether the release already fixes them.
	stale := []string{}
	if len(changed) > 0 {
		pending, err := s.store.ListReports(ctx, model.StatusPending, 1000)
		if err != nil {
			s.internalError(w, r, "publish dataset: list reports", err)
			return
		}
		for _, rep := range pending {
			if rep.LineID != "" && rep.DataVersion < v && slices.Contains(changed, rep.LineID) {
				stale = append(stale, rep.ID)
			}
		}
	}
	s.log.Info("dataset published", "version", v, "contributions", len(markedIDs), "devices_notified", notified)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":                          v,
		"published_contributions":          markedIDs,
		"skipped_contributions":            skipped,
		"devices_notified":                 notified,
		"announced":                        announced,
		"push_enabled":                     s.push.Enabled(),
		"pending_reports_on_changed_lines": stale,
	})
}
