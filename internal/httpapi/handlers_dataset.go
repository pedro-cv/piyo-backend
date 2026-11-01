// Dataset endpoints: the manifest of the latest release, versioned files
// served from RELEASES_DIR, the legacy /v1/data/{file} alias, and the
// MapLibre style whose URLs point at this deployment's tile server.
//
// Releases are immutable and named by version, so every file is served with
// a one-year immutable Cache-Control; only manifest.json and latest change.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// publicBase returns the absolute base URL of this API as seen by the
// client: PUBLIC_BASE_URL when configured, else derived from the request
// (X-Forwarded-Proto/Host set by Caddy, or the Host header).
func (s *Server) publicBase(r *http.Request) string {
	if s.cfg.PublicBaseURL != "" {
		return strings.TrimRight(s.cfg.PublicBaseURL, "/")
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

// latestManifest returns the newest release manifest with absolute URLs, or
// the legacy embedded-bundle manifest when no release directory exists.
func (s *Server) latestManifest(r *http.Request) (any, int, bool) {
	if s.catalog != nil {
		if m, err := s.catalog.Latest(); err == nil {
			abs := m.WithBase(s.publicBase(r) + "/v1/datasets/" + strconv.Itoa(m.DatasetVersion))
			abs.MinAppVersion = s.cfg.MinAppVersion
			return abs, m.DatasetVersion, true
		}
	}
	return s.manifest, s.manifest.DataVersion, false
}

// handleManifest: GET /v1/manifest
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	m, _, _ := s.latestManifest(r)
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, m)
}

// handleDatasetVersions: GET /v1/datasets
func (s *Server) handleDatasetVersions(w http.ResponseWriter, r *http.Request) {
	versions := []int{}
	if s.catalog != nil {
		versions = s.catalog.Versions()
	}
	latest := 0
	if len(versions) > 0 {
		latest = versions[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"latest": latest, "versions": versions})
}

// handleDatasetFile: GET /v1/datasets/{version}/{file}
func (s *Server) handleDatasetFile(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.Atoi(r.PathValue("version"))
	name := r.PathValue("file")
	if err != nil || v <= 0 || s.catalog == nil || name != path.Base(name) {
		writeError(w, http.StatusNotFound, "not_found", "no such dataset file", nil)
		return
	}
	if name == "manifest.json" {
		m, err := s.catalog.Manifest(v)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such dataset version", nil)
			return
		}
		abs := m.WithBase(s.publicBase(r) + "/v1/datasets/" + strconv.Itoa(v))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		writeJSON(w, http.StatusOK, abs)
		return
	}
	p := s.catalog.FilePath(v, name)
	if p == "" {
		writeError(w, http.StatusNotFound, "not_found", "no such dataset file", nil)
		return
	}
	s.serveReleaseFile(w, r, p, name)
}

func (s *Server) serveReleaseFile(w http.ResponseWriter, r *http.Request, p, name string) {
	f, err := os.Open(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such dataset file", nil)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internalError(w, r, "dataset stat", err)
		return
	}
	switch {
	case strings.HasSuffix(name, ".json.gz"):
		// Pre-compressed JSON: tell clients what it is so OkHttp/curl can inflate it.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("X-Piyo-Raw-Encoding", "gzip")
	case strings.HasSuffix(name, ".json"):
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("ETag", `"`+name+`-`+strconv.FormatInt(st.Size(), 10)+`"`)
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// handleData: GET /v1/data/{file} (legacy). With a release directory it
// resolves names inside the latest release; otherwise the embedded bundle.
func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name == "" || strings.ContainsAny(name, `/\`) || name != path.Base(name) || strings.HasPrefix(name, ".") {
		writeError(w, http.StatusNotFound, "not_found", "no such data file", nil)
		return
	}
	if s.catalog != nil {
		if m, err := s.catalog.Latest(); err == nil {
			if p := s.catalog.FilePath(m.DatasetVersion, name); p != "" {
				s.serveReleaseFile(w, r, p, name)
				return
			}
		}
	}
	s.handleEmbeddedData(w, r, name)
}

// mapStyle is the MapLibre style served at /v1/map/style.json: Protomaps
// basemap layers (generated at deploy time into deploy/tiles/style/) with
// the source, glyph and sprite URLs rewritten to this deployment.
type mapStyle struct {
	Version int                        `json:"version"`
	Name    string                     `json:"name"`
	Glyphs  string                     `json:"glyphs"`
	Sprite  any                        `json:"sprite"`
	Sources map[string]json.RawMessage `json:"sources"`
	Layers  json.RawMessage            `json:"layers"`
}

// handleStyle: GET /v1/map/style.json?theme=light|dark
func (s *Server) handleStyle(w http.ResponseWriter, r *http.Request) {
	theme := r.URL.Query().Get("theme")
	if theme != "dark" {
		theme = "light"
	}
	raw, err := os.ReadFile(path.Join(s.cfg.StyleDir, "style-"+theme+".json"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "map style not generated yet (see deploy/tiles/README.md)", nil)
		return
	}
	var st mapStyle
	if err := json.Unmarshal(raw, &st); err != nil {
		s.internalError(w, r, "style parse", err)
		return
	}
	base := s.publicBase(r)
	tiles := s.cfg.TilesPublicURL
	if tiles == "" {
		tiles = base + "/tiles"
	}
	assets := s.cfg.AssetsPublicURL
	if assets == "" {
		assets = base + "/assets"
	}
	// Protomaps basemaps-assets layout: fonts/<stack>/<range>.pbf, sprites/v4/<flavor>.{json,png}
	st.Glyphs = assets + "/fonts/{fontstack}/{range}.pbf"
	st.Sprite = assets + "/sprites/v4/" + theme
	src, _ := json.Marshal(map[string]any{
		"type":        "vector",
		"tiles":       []string{tiles + "/santa-cruz/{z}/{x}/{y}"},
		"minzoom":     0,
		"maxzoom":     15,
		"attribution": "© OpenStreetMap contributors · Protomaps",
	})
	st.Sources = map[string]json.RawMessage{"protomaps": src}
	st.Name = "Piyo " + theme
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, st)
}

// handleMapInfo: GET /v1/map — where the offline PMTiles lives and its size.
func (s *Server) handleMapInfo(w http.ResponseWriter, r *http.Request) {
	tiles := s.cfg.TilesPublicURL
	if tiles == "" {
		tiles = s.publicBase(r) + "/tiles"
	}
	info := map[string]any{
		"style_light": s.publicBase(r) + "/v1/map/style.json?theme=light",
		"style_dark":  s.publicBase(r) + "/v1/map/style.json?theme=dark",
		"tiles":       tiles + "/santa-cruz/{z}/{x}/{y}",
		"pmtiles":     tiles + "/santa-cruz.pmtiles",
		"checked_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if st, err := os.Stat(path.Join(s.cfg.TilesDir, "santa-cruz.pmtiles")); err == nil {
		info["pmtiles_bytes"] = st.Size()
		info["pmtiles_updated_at"] = st.ModTime().UTC().Format(time.RFC3339)
		info["pmtiles_mb"] = fmt.Sprintf("%.1f", float64(st.Size())/1024/1024)
	}
	writeJSON(w, http.StatusOK, info)
}
