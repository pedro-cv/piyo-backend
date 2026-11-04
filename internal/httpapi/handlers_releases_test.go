package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"piyo-backend/internal/config"
	"piyo-backend/internal/dataset"
	"piyo-backend/internal/push"
	"piyo-backend/internal/ratelimit"
	"piyo-backend/internal/store"
)

func releaseTestServer(t *testing.T, dir string, sender push.Sender) http.Handler {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevMode, cfg.AdminToken, cfg.HashSalt = true, "admin-test", "salt"
	cfg.Store, cfg.IPRatePerMinute, cfg.ReleasesDir, cfg.MaxContributionsPerMonth = config.StoreMemory, 1000, dir, 2
	return NewHandlerWithPush(cfg, store.NewMemory(), ratelimit.New(1000), slog.New(slog.NewTextHandler(io.Discard, nil)), sender)
}

// twoRoutesBody is one request carrying two new routes.
func twoRoutesBody() string {
	var a, b map[string]any
	_ = json.Unmarshal([]byte(contributionBody("u-t1-a1b2", okPolyline)), &a)
	_ = json.Unmarshal([]byte(contributionBody("u-t2-c3d4", okPolyline)), &b)
	a["lines"] = append(a["lines"].([]any), b["lines"].([]any)...)
	a["variants"] = append(a["variants"].([]any), b["variants"].([]any)...)
	for k, v := range b["edits"].(map[string]any) {
		a["edits"].(map[string]any)[k] = v
	}
	out, _ := json.Marshal(a)
	return string(out)
}

func TestContributionsSplitWithdrawAndPublish(t *testing.T) {
	dir := t.TempDir()
	// Release v15 on disk: one official line.
	base := &dataset.Package{DatasetVersion: 15, PackageFormat: dataset.PackageFormat, FareBs: "3.50",
		Lines:    []dataset.Line{{LineID: "1", Display: "1", Mode: "micro", Color: "#112233", VariantIDs: []string{"1-ida"}}},
		Variants: []dataset.Variant{{VariantID: "1-ida", LineID: "1", Direction: "ida", Polyline: okPolyline, StopIDs: []string{}}},
		Stops:    []dataset.Stop{}}
	if _, err := dataset.Write(dir, base, "1"); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{ch: make(chan struct{}, 32)}
	h := releaseTestServer(t, dir, sender)
	dev := map[string]string{HeaderInstallID: "device-0001"}
	other := map[string]string{HeaderInstallID: "device-0002"}
	admin := map[string]string{HeaderAdminToken: "admin-test"}

	// Two routes in one request: two rows, one batch, one unit of quota.
	rec, out := do(h, "POST", "/v1/contributions", twoRoutesBody(), dev)
	if rec.Code != 201 || out["remaining"].(float64) != 1 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	created := out["contributions"].([]any)
	if len(created) != 2 || out["batch_id"] == "" {
		t.Fatalf("created %v", out)
	}
	idA := created[0].(map[string]any)["id"].(string)
	idB := created[1].(map[string]any)["id"].(string)
	if idA == idB || created[0].(map[string]any)["route_id"] != "u-t1-a1b2" || out["id"] != idA {
		t.Fatalf("ids %v", created)
	}
	rec, out = do(h, "GET", "/v1/contributions/mine", "", dev)
	mine := out["contributions"].([]any)
	if rec.Code != 200 || len(mine) != 2 {
		t.Fatalf("mine: %v", out)
	}
	for _, m := range mine {
		m := m.(map[string]any)
		if len(m["route_ids"].([]any)) != 1 || m["batch_id"] != out["batch_id"] && m["batch_id"] != mine[0].(map[string]any)["batch_id"] {
			t.Fatalf("row %v", m)
		}
	}
	if mine[0].(map[string]any)["batch_id"] != mine[1].(map[string]any)["batch_id"] || mine[0].(map[string]any)["batch_id"] == "" {
		t.Fatalf("rows not in one batch: %v", mine)
	}
	// Each stored payload holds only its own route.
	_, out = do(h, "GET", "/v1/admin/contributions/"+idB, "", admin)
	pb := out["contribution"].(map[string]any)["payload"].(map[string]any)
	if len(pb["lines"].([]any)) != 1 || len(pb["variants"].([]any)) != 1 || pb["edits"].(map[string]any)["u-t2-c3d4"] == nil || pb["base_dataset_version"].(float64) != 15 {
		t.Fatalf("payload of B: %v", pb)
	}

	// Withdraw B (pending): 204; a second time or from another device: 404.
	if rec, _ := do(h, "DELETE", "/v1/contributions/"+idB, "", other); rec.Code != 404 {
		t.Fatalf("other device withdraw: %d", rec.Code)
	}
	if rec, _ := do(h, "DELETE", "/v1/contributions/"+idB, "", dev); rec.Code != 204 {
		t.Fatalf("withdraw: %d", rec.Code)
	}
	if rec, _ := do(h, "DELETE", "/v1/contributions/"+idB, "", dev); rec.Code != 404 {
		t.Fatalf("withdraw twice: %d", rec.Code)
	}
	// The batch still counts: A remains.
	if _, out := do(h, "GET", "/v1/contributions/quota", "", dev); out["remaining"].(float64) != 1 {
		t.Fatalf("quota after withdraw: %v", out)
	}

	// Accept A; reviewed rows cannot be withdrawn.
	if rec, _ := do(h, "POST", "/v1/admin/contributions/"+idA+"/accept", `{"note":"ok"}`, admin); rec.Code != 200 {
		t.Fatalf("accept: %d", rec.Code)
	}
	sender.drain()
	if rec, out := do(h, "DELETE", "/v1/contributions/"+idA, "", dev); rec.Code != 409 || out["error"].(map[string]any)["code"] != "not_pending" {
		t.Fatalf("withdraw reviewed: %d %v", rec.Code, out)
	}
	if rec, _ := do(h, "PUT", "/v1/push/token", `{"token":"fcm:`+strings.Repeat("a", 150)+`"}`, dev); rec.Code != 204 {
		t.Fatalf("token: %d", rec.Code)
	}
	// A report about the line the release will add, made against v15.
	if rec, _ := do(h, "POST", "/v1/reports", `{"type":"stop","lat":-17.7834,"lng":-63.1821,"line_id":"u-t1-a1b2","app_version":"1","data_version":15}`, dev); rec.Code != 201 {
		t.Fatalf("report: %d", rec.Code)
	}

	// What the next release can include.
	_, out = do(h, "GET", "/v1/admin/contributions?status=accepted&published=false", "", admin)
	if out["count"].(float64) != 1 {
		t.Fatalf("publishable: %v", out)
	}

	// piyo-release, in process: merge the accepted payload into v16.
	_, out = do(h, "GET", "/v1/admin/contributions/"+idA, "", admin)
	raw, _ := json.Marshal(out["contribution"].(map[string]any)["payload"])
	var dc dataset.Contribution
	if err := json.Unmarshal(raw, &dc); err != nil {
		t.Fatal(err)
	}
	dc.ID = idA
	basePkg, _, err := dataset.ReadPackage(dir, 15)
	if err != nil {
		t.Fatal(err)
	}
	pkg, rep, err := dataset.MergeContributions(basePkg, []dataset.Contribution{dc}, dataset.MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Publishing before the release is deployed: 404.
	if rec, _ := do(h, "POST", "/v1/admin/datasets/16/publish", "", admin); rec.Code != 404 {
		t.Fatalf("publish undeployed: %d", rec.Code)
	}
	if _, err := dataset.WriteRelease(dir, pkg, "1", &dataset.ReleaseChanges{BaseVersion: 15, ContributionIDs: rep.ContributionIDs(),
		LinesAdded: rep.LinesAdded, LinesUpdated: rep.LinesUpdated}); err != nil {
		t.Fatal(err)
	}
	if _, out := do(h, "GET", "/v1/manifest", "", nil); out["dataset_version"].(float64) != 16 {
		t.Fatalf("manifest: %v", out)
	}
	// Only the latest release may be published.
	if rec, _ := do(h, "POST", "/v1/admin/datasets/15/publish", "", admin); rec.Code != 409 {
		t.Fatalf("publish old: %d", rec.Code)
	}
	if rec, _ := do(h, "POST", "/v1/admin/datasets/16/publish", "", nil); rec.Code != 401 {
		t.Fatalf("publish without token: %d", rec.Code)
	}

	rec, out = do(h, "POST", "/v1/admin/datasets/16/publish", "", admin)
	if rec.Code != 200 || len(out["published_contributions"].([]any)) != 1 || out["devices_notified"].(float64) != 1 || out["announced"] != true {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	if stale := out["pending_reports_on_changed_lines"].([]any); len(stale) != 1 {
		t.Fatalf("stale reports: %v", stale)
	}
	sender.mu.Lock()
	var personal, topic *push.Message
	for i := range sender.sent {
		m := &sender.sent[i]
		switch {
		case m.Data["type"] == "contribution_published":
			personal = m
		case m.Topic == TopicDataset:
			topic = m
		}
	}
	sender.mu.Unlock()
	if personal == nil || personal.Data["version"] != "16" || personal.Data["count"] != "1" || personal.Token == "" {
		t.Fatalf("personal push: %+v", personal)
	}
	if topic == nil || topic.Data["version"] != "16" {
		t.Fatalf("topic push: %+v", topic)
	}

	// Idempotent: nothing is marked or notified twice.
	sender.drain()
	rec, out = do(h, "POST", "/v1/admin/datasets/16/publish", `{"announce":false}`, admin)
	if rec.Code != 200 || len(out["published_contributions"].([]any)) != 0 || out["devices_notified"].(float64) != 0 || len(out["skipped_contributions"].([]any)) != 1 {
		t.Fatalf("publish again: %s", rec.Body.String())
	}
	_, out = do(h, "GET", "/v1/contributions/mine", "", dev)
	if m := out["contributions"].([]any)[0].(map[string]any); m["published_version"].(float64) != 16 {
		t.Fatalf("mine after publish: %v", m)
	}
	if _, out := do(h, "GET", "/v1/admin/contributions?status=accepted&published=false", "", admin); out["count"].(float64) != 0 {
		t.Fatalf("still publishable: %v", out)
	}
	if rec, _ := do(h, "GET", "/v1/admin/contributions?published=maybe", "", admin); rec.Code != 400 {
		t.Fatalf("bad filter: %d", rec.Code)
	}
}

// drain empties the notification channel without blocking.
func (f *fakeSender) drain() {
	for {
		select {
		case <-f.ch:
		default:
			return
		}
	}
}
