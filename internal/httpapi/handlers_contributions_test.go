package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"piyo-backend/internal/config"
	"piyo-backend/internal/dataset"
	"piyo-backend/internal/ratelimit"
	"piyo-backend/internal/store"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevMode = true
	cfg.AdminToken = "admin-test"
	cfg.HashSalt = "salt"
	cfg.Store = config.StoreMemory
	cfg.MaxContributionsPerMonth = 2
	cfg.IPRatePerMinute = 1000
	if cfg.ManifestDataURL == "" {
		cfg.ManifestDataURL = "http://localhost/v1/data/bundle.json"
	}
	return NewHandler(cfg, store.NewMemory(), ratelimit.New(1000), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// A short route inside the metro bounds (Plaza 24 de Septiembre → 2° anillo).
var okPolyline = dataset.EncodePolyline([]dataset.Point{{Lat: -17.7834, Lng: -63.1821}, {Lat: -17.7850, Lng: -63.1900}, {Lat: -17.7900, Lng: -63.1950}})

func contributionBody(lineID, polyline string) string {
	b, _ := json.Marshal(map[string]any{
		"provider_name": "Mis rutas", "base_dataset_version": 15, "app_version": "1.0", "comment": "nuevo trufi",
		"lines":    []map[string]any{{"line_id": lineID, "display": "T1", "name": "Trufi 1", "mode": "trufi", "color": "#AA2233"}},
		"variants": []map[string]any{{"variant_id": lineID + ":ida", "line_id": lineID, "direction": "ida", "headsign": "Plan 3000", "polyline": polyline}},
		"edits":    map[string]any{lineID: map[string]any{"op": "add", "based_on_version": 15}},
	})
	return string(b)
}

func do(h http.Handler, method, path, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestContributionsLifecycle(t *testing.T) {
	h := testServer(t)
	dev := map[string]string{HeaderInstallID: "device-0001"}
	admin := map[string]string{HeaderAdminToken: "admin-test"}

	rec, out := do(h, "GET", "/v1/contributions/quota", "", dev)
	if rec.Code != 200 || out["limit"].(float64) != 2 || out["remaining"].(float64) != 2 {
		t.Fatalf("quota: %d %v", rec.Code, out)
	}

	rec, out = do(h, "POST", "/v1/contributions", contributionBody("u-t1-a1b2", okPolyline), dev)
	if rec.Code != 201 || out["remaining"].(float64) != 1 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	id := out["id"].(string)

	// Validation: a point outside the metro area is refused.
	bad := dataset.EncodePolyline([]dataset.Point{{Lat: -17.78, Lng: -63.18}, {Lat: -16.5, Lng: -68.1}})
	rec, out = do(h, "POST", "/v1/contributions", contributionBody("u-t2-c3d4", bad), dev)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "metro area") {
		t.Fatalf("bounds validation: %d %s", rec.Code, rec.Body.String())
	}

	// Second one fits the monthly quota, the third does not.
	if rec, _ = do(h, "POST", "/v1/contributions", contributionBody("u-t2-c3d4", okPolyline), dev); rec.Code != 201 {
		t.Fatalf("second create: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ = do(h, "POST", "/v1/contributions", contributionBody("u-t3-e5f6", okPolyline), dev); rec.Code != 429 {
		t.Fatalf("quota: want 429, got %d %s", rec.Code, rec.Body.String())
	}

	// The device sees its statuses without payloads.
	rec, out = do(h, "GET", "/v1/contributions/mine", "", dev)
	if rec.Code != 200 || out["count"].(float64) != 2 {
		t.Fatalf("mine: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "polyline") {
		t.Fatal("mine must not include payloads")
	}

	// Admin: list pending, get full payload, accept with a note.
	rec, out = do(h, "GET", "/v1/admin/contributions", "", admin)
	if rec.Code != 200 || out["count"].(float64) != 2 {
		t.Fatalf("admin list: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = do(h, "GET", "/v1/admin/contributions/"+id, "", admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), okPolyline[:8]) {
		t.Fatalf("admin get: %d %s", rec.Code, rec.Body.String())
	}
	rec, out = do(h, "POST", "/v1/admin/contributions/"+id+"/accept", `{"note":"verificado en campo"}`, admin)
	if rec.Code != 200 {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	rec, out = do(h, "GET", "/v1/contributions/mine", "", dev)
	list := out["contributions"].([]any)
	found := false
	for _, c := range list {
		m := c.(map[string]any)
		if m["id"] == id && m["status"] == "accepted" && m["review_note"] == "verificado en campo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("accepted status not visible to the device: %s", rec.Body.String())
	}

	// Without the admin token the review endpoints are closed.
	if rec, _ = do(h, "GET", "/v1/admin/contributions", "", dev); rec.Code != 401 {
		t.Fatalf("admin without token: %d", rec.Code)
	}
}

func TestContributionBodyCap(t *testing.T) {
	h := testServer(t)
	dev := map[string]string{HeaderInstallID: "device-0002"}
	// 100 KB is over the global 64 KB cap but under the contributions cap: must be parsed, not cut.
	big := contributionBody("u-big-0001", okPolyline)
	big = big[:len(big)-1] + `,"padding":"` + strings.Repeat("x", 100<<10) + `"}`
	rec, _ := do(h, "POST", "/v1/contributions", big, dev)
	if rec.Code != 201 {
		t.Fatalf("100 KB contribution: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = do(h, "POST", "/v1/reports", string(bytes.Repeat([]byte("x"), 100<<10)), dev)
	if rec.Code != 400 && rec.Code != 413 {
		t.Fatalf("reports keep the small cap: %d", rec.Code)
	}
}
