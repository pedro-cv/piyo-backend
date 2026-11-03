package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"piyo-backend/internal/config"
	"piyo-backend/internal/push"
	"piyo-backend/internal/ratelimit"
	"piyo-backend/internal/store"
)

// fakeSender records messages; token "gone" behaves like an uninstalled app.
type fakeSender struct {
	mu   sync.Mutex
	sent []push.Message
	ch   chan struct{}
}

func (f *fakeSender) Enabled() bool { return true }
func (f *fakeSender) Send(_ context.Context, m push.Message) error {
	defer func() { f.ch <- struct{}{} }()
	if m.Token == strings.Repeat("g", 40) {
		return push.ErrUnregistered
	}
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	return nil
}

func (f *fakeSender) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("push not sent")
	}
}

func pushTestServer(t *testing.T, sender push.Sender) (http.Handler, *store.Memory) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DevMode, cfg.AdminToken, cfg.HashSalt = true, "admin-test", "salt"
	cfg.Store, cfg.IPRatePerMinute, cfg.ReleasesDir = config.StoreMemory, 1000, ""
	st := store.NewMemory()
	return NewHandlerWithPush(cfg, st, ratelimit.New(1000), slog.New(slog.NewTextHandler(io.Discard, nil)), sender), st
}

func TestPushTokenRegistration(t *testing.T) {
	h, st := pushTestServer(t, nil)
	dev := map[string]string{HeaderInstallID: "device-push-1"}
	hash := HashInstallID("salt", "device-push-1")
	token := "fcm:" + strings.Repeat("a", 150)

	if rec, _ := do(h, "PUT", "/v1/push/token", `{"token":"`+token+`"}`, nil); rec.Code != 400 {
		t.Fatalf("missing install id: %d", rec.Code)
	}
	if rec, out := do(h, "PUT", "/v1/push/token", `{"token":"short"}`, dev); rec.Code != 400 {
		t.Fatalf("bad token: %d %v", rec.Code, out)
	}
	if rec, _ := do(h, "PUT", "/v1/push/token", `{"token":"`+token+`"}`, dev); rec.Code != 204 {
		t.Fatalf("set: %d", rec.Code)
	}
	if got, _ := st.GetPushToken(context.Background(), hash); got != token {
		t.Fatalf("stored token %q", got)
	}
	if rec, _ := do(h, "DELETE", "/v1/push/token", "", dev); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if got, _ := st.GetPushToken(context.Background(), hash); got != "" {
		t.Fatalf("token survived delete")
	}
	// Push disabled: the admin broadcast says so.
	admin := map[string]string{HeaderAdminToken: "admin-test"}
	if rec, out := do(h, "POST", "/v1/admin/push/dataset", `{"version":16}`, admin); rec.Code != 503 {
		t.Fatalf("disabled broadcast: %d %v", rec.Code, out)
	}
}

func TestPushOnContributionReview(t *testing.T) {
	fs := &fakeSender{ch: make(chan struct{}, 4)}
	h, st := pushTestServer(t, fs)
	dev := map[string]string{HeaderInstallID: "device-push-2"}
	admin := map[string]string{HeaderAdminToken: "admin-test"}
	token := strings.Repeat("t", 160)

	if rec, _ := do(h, "PUT", "/v1/push/token", `{"token":"`+token+`"}`, dev); rec.Code != 204 {
		t.Fatalf("set: %d", rec.Code)
	}
	rec, out := do(h, "POST", "/v1/contributions", contributionBody("u-p1-a1b2", okPolyline), dev)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	id := out["id"].(string)
	if rec, _ := do(h, "POST", "/v1/admin/contributions/"+id+"/accept", "", admin); rec.Code != 200 {
		t.Fatalf("accept: %d", rec.Code)
	}
	fs.wait(t)
	fs.mu.Lock()
	m := fs.sent[0]
	fs.mu.Unlock()
	if m.Token != token || m.Data["type"] != "contribution_accepted" || m.Data["id"] != id || !m.High {
		t.Fatalf("message: %+v", m)
	}

	// An unregistered token is removed after the failed send.
	gone := strings.Repeat("g", 40)
	if rec, _ := do(h, "PUT", "/v1/push/token", `{"token":"`+gone+`"}`, dev); rec.Code != 204 {
		t.Fatal("set gone token")
	}
	if rec, _ := do(h, "POST", "/v1/admin/contributions/"+id+"/reject", "", admin); rec.Code != 200 {
		t.Fatalf("reject: %d", rec.Code)
	}
	fs.wait(t)
	hash := HashInstallID("salt", "device-push-2")
	deadline := time.Now().Add(2 * time.Second)
	for {
		tok, _ := st.GetPushToken(context.Background(), hash)
		if tok == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale token not deleted")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Release broadcast to the topic.
	rec, out = do(h, "POST", "/v1/admin/push/dataset", `{"version":16}`, admin)
	if rec.Code != 200 || out["version"].(float64) != 16 {
		t.Fatalf("broadcast: %d %v", rec.Code, out)
	}
	fs.wait(t)
	fs.mu.Lock()
	last := fs.sent[len(fs.sent)-1]
	fs.mu.Unlock()
	if last.Topic != TopicDataset || last.Data["version"] != "16" {
		t.Fatalf("topic message: %+v", last)
	}
	if rec, _ := do(h, "POST", "/v1/admin/push/dataset", `{}`, nil); rec.Code != 401 {
		t.Fatalf("admin route without token: %d", rec.Code)
	}
}

func TestPushTokenPurge(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now()
	_ = st.SetPushToken(ctx, "old", "x", now.Add(-PushTokenMaxAge-time.Hour))
	_ = st.SetPushToken(ctx, "new", "y", now)
	n, err := st.DeletePushTokensBefore(ctx, now.Add(-PushTokenMaxAge))
	if err != nil || n != 1 {
		t.Fatalf("purged %d err %v", n, err)
	}
	if tok, _ := st.GetPushToken(ctx, "new"); tok != "y" {
		t.Fatal("fresh token purged")
	}
}
