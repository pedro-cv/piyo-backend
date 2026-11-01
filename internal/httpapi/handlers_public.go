// Public, unauthenticated endpoints: health, manifest and the embedded data
// bundles.
package httpapi

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"piyo-backend/static"
)

// handleHealth: GET /v1/health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, version, fromRelease := s.latestManifest(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"mode":            s.cfg.Mode(),
		"store":           s.cfg.StoreKind(),
		"dataset_version": version,
		"dataset_source":  map[bool]string{true: "releases", false: "embedded"}[fromRelease],
		"time":            time.Now().UTC().Format(time.RFC3339),
	})
}

// handleEmbeddedData serves an embedded sample bundle (fallback when no
// release directory is configured). Bundles are versioned by name, so they
// are cached forever.
func (s *Server) handleEmbeddedData(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(static.FS, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such data file", nil)
		return
	}
	ct := "application/octet-stream"
	if strings.HasSuffix(name, ".json") {
		ct = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if name == bundleNameFromURL(s.cfg.ManifestDataURL) && s.manifest.SHA256 != "" {
		w.Header().Set("ETag", `"`+s.manifest.SHA256+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
