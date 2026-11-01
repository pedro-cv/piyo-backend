// Package httpapi exposes the Piyo REST API (/v1/...). This file wires the
// router, the middleware chain and the JSON/error helpers shared by the
// handlers_*.go files.
//
// Middleware order (outermost first): recover -> request id -> access log ->
// CORS -> IP rate limit -> body limit (64 KB) -> router.
//
// Privacy: the access log records method, path, status, duration and the
// request id only. Client IPs are used solely as the rate-limiter key and
// are never logged or persisted; user agents are never read.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"piyo-backend/internal/config"
	"piyo-backend/internal/dataset"
	"piyo-backend/internal/model"
	"piyo-backend/internal/ratelimit"
	"piyo-backend/internal/store"
	"piyo-backend/static"
)

// maxBodyBytes caps every request body.
const maxBodyBytes = 64 << 10

// Server holds the dependencies shared by all handlers.
type Server struct {
	cfg      config.Config
	store    store.Store
	limiter  *ratelimit.Limiter
	log      *slog.Logger
	manifest model.Manifest   // legacy embedded-bundle manifest (fallback)
	catalog  *dataset.Catalog // releases on disk (nil when RELEASES_DIR is empty)
}

// NewHandler builds the complete http.Handler for the API.
func NewHandler(cfg config.Config, st store.Store, limiter *ratelimit.Limiter, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, store: st, limiter: limiter, log: logger}
	s.manifest = buildManifest(cfg, logger)
	if cfg.ReleasesDir != "" {
		s.catalog = dataset.NewCatalog(cfg.ReleasesDir)
		if m, err := s.catalog.Latest(); err == nil {
			logger.Info("dataset releases", "dir", cfg.ReleasesDir, "latest", m.DatasetVersion)
		} else {
			logger.Warn("dataset releases: none found; serving the embedded sample bundle", "dir", cfg.ReleasesDir)
		}
	}

	mux := http.NewServeMux()
	s.routes(mux)

	var h http.Handler = mux
	h = s.bodyLimit(h)
	h = s.rateLimit(h)
	h = s.cors(h)
	h = s.accessLog(h)
	h = requestID(h)
	h = s.recoverer(h)
	return h
}

// routes registers every endpoint on mux.
func (s *Server) routes(mux *http.ServeMux) {
	// Public
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/manifest", s.handleManifest)
	mux.HandleFunc("GET /v1/data/{file}", s.handleData)
	mux.HandleFunc("GET /v1/datasets", s.handleDatasetVersions)
	mux.HandleFunc("GET /v1/datasets/{version}/{file}", s.handleDatasetFile)
	mux.HandleFunc("GET /v1/map", s.handleMapInfo)
	mux.HandleFunc("GET /v1/map/style.json", s.handleStyle)

	// Device (X-Install-Id)
	mux.HandleFunc("GET /v1/reports/quota", s.handleReportQuota)
	mux.HandleFunc("POST /v1/reports", s.handleCreateReport)
	mux.HandleFunc("POST /v1/nps", s.handleCreateNPS)
	mux.HandleFunc("GET /v1/suggestions/quota", s.handleSuggestionQuota)
	mux.HandleFunc("POST /v1/suggestions", s.handleCreateSuggestion)
	mux.HandleFunc("GET /v1/contributions/quota", s.handleContributionQuota)
	mux.HandleFunc("GET /v1/contributions/mine", s.handleMyContributions)
	mux.HandleFunc("POST /v1/contributions", s.handleCreateContribution)

	// Admin (X-Admin-Token)
	admin := s.requireAdmin
	mux.HandleFunc("GET /v1/admin/reports", admin(s.handleAdminListReports))
	mux.HandleFunc("POST /v1/admin/reports/{id}/accept", admin(s.handleAdminAcceptReport))
	mux.HandleFunc("POST /v1/admin/reports/{id}/reject", admin(s.handleAdminRejectReport))
	mux.HandleFunc("GET /v1/admin/suggestions", admin(s.handleAdminListSuggestions))
	mux.HandleFunc("POST /v1/admin/suggestions/{id}/accept", admin(s.handleAdminAcceptSuggestion))
	mux.HandleFunc("POST /v1/admin/suggestions/{id}/reject", admin(s.handleAdminRejectSuggestion))
	mux.HandleFunc("GET /v1/admin/contributions", admin(s.handleAdminListContributions))
	mux.HandleFunc("GET /v1/admin/contributions/{id}", admin(s.handleAdminGetContribution))
	mux.HandleFunc("POST /v1/admin/contributions/{id}/accept", admin(s.handleAdminAcceptContribution))
	mux.HandleFunc("POST /v1/admin/contributions/{id}/reject", admin(s.handleAdminRejectContribution))
	mux.HandleFunc("GET /v1/admin/datasets", admin(s.handleAdminDatasets))
	mux.HandleFunc("GET /v1/admin/blocks", admin(s.handleAdminListBlocks))
	mux.HandleFunc("POST /v1/admin/blocks", admin(s.handleAdminCreateBlock))
	mux.HandleFunc("DELETE /v1/admin/blocks/{device_hash}", admin(s.handleAdminDeleteBlock))
	mux.HandleFunc("GET /v1/admin/nps.csv", admin(s.handleAdminExportNPSCSV))
	mux.HandleFunc("GET /v1/admin/reports.json", admin(s.handleAdminExportReportsJSON))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint", nil)
	})
}

// buildManifest derives the manifest from config plus the embedded bundle
// named by the last path segment of MANIFEST_DATA_URL.
func buildManifest(cfg config.Config, logger *slog.Logger) model.Manifest {
	m := model.Manifest{
		DataVersion:   cfg.ManifestDataVersion,
		URL:           cfg.ManifestDataURL,
		MinAppVersion: cfg.MinAppVersion,
		GeneratedAt:   time.Now().UTC(),
		FareBs:        cfg.FareBs,
	}
	name := bundleNameFromURL(cfg.ManifestDataURL)
	if name == "" {
		logger.Warn("manifest: cannot derive bundle file name from MANIFEST_DATA_URL", "url", cfg.ManifestDataURL)
		return m
	}
	data, err := fs.ReadFile(static.FS, name)
	if err != nil {
		logger.Warn("manifest: embedded bundle not found; bytes/sha256 left empty", "file", name, "err", err)
		return m
	}
	sum := sha256.Sum256(data)
	m.Bytes = int64(len(data))
	m.SHA256 = hex.EncodeToString(sum[:])

	// Prefer the bundle's own generated_at when it has one.
	var head struct {
		GeneratedAt time.Time `json:"generated_at"`
	}
	if json.Unmarshal(data, &head) == nil && !head.GeneratedAt.IsZero() {
		m.GeneratedAt = head.GeneratedAt.UTC()
	}
	return m
}

func bundleNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		return ""
	}
	return name
}

// --- middleware ------------------------------------------------------------

type ctxKey int

const ctxRequestID ctxKey = iota

// requestID attaches a random id to the context and the response.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := randomHex(8)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

// recoverer converts panics into 500s so a bad request never kills the
// process.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec) // let net/http handle client aborts
				}
				s.log.Error("panic recovered", "request_id", requestIDFrom(r.Context()), "panic", fmt.Sprint(rec))
				writeError(w, http.StatusInternalServerError, "internal", "internal error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter captures the status code for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sw *statusWriter) WriteHeader(code int) {
	if sw.status == 0 {
		sw.status = code
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += n
	return n, err
}

// accessLog logs one line per request: no IP, no user agent.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		s.log.Info("request",
			"request_id", requestIDFrom(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", float64(time.Since(start).Microseconds())/1000,
		)
	})
}

// cors answers preflights and sets the allow-origin header for configured
// origins ("*" allows everyone, empty list disables CORS).
func (s *Server) cors(next http.Handler) http.Handler {
	allowAll := false
	allowed := make(map[string]bool, len(s.cfg.CORSAllowedOrigins))
	for _, o := range s.cfg.CORSAllowedOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			switch {
			case allowAll:
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case allowed[origin]:
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			if allowAll || allowed[origin] {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Install-Id, X-Admin-Token")
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimit applies the per-IP token bucket.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter != nil && !s.limiter.Allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests from this address, slow down", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the first X-Forwarded-For entry when present, else the
// RemoteAddr host. The value is only used in memory as a limiter key.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// bodyLimit caps request bodies at maxBodyBytes, except route contributions,
// which carry whole polylines and get model.MaxContributionBody.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			limit := int64(maxBodyBytes)
			if r.Method == http.MethodPost && r.URL.Path == "/v1/contributions" {
				limit = model.MaxContributionBody
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ----------------------------------------------------------

// writeJSON encodes v with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already out; nothing useful left to do.
		_ = err
	}
}

// writeError writes the error envelope {"error":{"code","message"}} plus any
// extra top-level fields (e.g. blocked_until, resets_at).
func writeError(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	body := map[string]any{
		"error": map[string]string{"code": code, "message": message},
	}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// decodeJSON parses the body into v. It returns a user-facing error message
// (and an HTTP status) when the body is not acceptable.
func decodeJSON(r *http.Request, v any) (int, error) {
	if r.Body == nil {
		return http.StatusBadRequest, errors.New("missing body")
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return http.StatusRequestEntityTooLarge, errors.New("body too large (max 64 KB)")
		}
		if errors.Is(err, io.EOF) {
			return http.StatusBadRequest, errors.New("empty body")
		}
		return http.StatusBadRequest, fmt.Errorf("invalid JSON: %v", err)
	}
	if dec.More() {
		return http.StatusBadRequest, errors.New("unexpected data after JSON object")
	}
	return 0, nil
}

// internalError logs the error with the request id and answers 500 without
// leaking details.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.log.Error(what, "request_id", requestIDFrom(r.Context()), "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error", nil)
}

// --- misc helpers ----------------------------------------------------------

// randomHex returns n random bytes as 2n hex characters.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is a broken platform; fall back to time so we
		// still return something unique-ish rather than crashing a request.
		return fmt.Sprintf("%0*x", 2*n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// newID returns prefix + 12 random hex characters (e.g. "r_3fa9c0b1d2e4").
func newID(prefix string) string { return prefix + randomHex(6) }

// utcDayStart returns 00:00:00 UTC of the day containing t.
func utcDayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// nextUTCMidnight returns the start of the next UTC calendar day.
func nextUTCMidnight(t time.Time) time.Time { return utcDayStart(t).Add(24 * time.Hour) }

// rfc3339 formats t for responses; nil-safe for optional times.
func rfc3339(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
