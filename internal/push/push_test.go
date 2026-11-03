package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewWithoutProjectIsNoop(t *testing.T) {
	s, err := New("", "")
	if err != nil || s.Enabled() {
		t.Fatalf("want disabled noop, got %T %v", s, err)
	}
	if err := s.Send(context.Background(), Message{Token: "x"}); err != nil {
		t.Fatal(err)
	}
}

// fakeGoogle serves the OAuth token endpoint and the FCM send endpoint.
func fakeGoogle(t *testing.T, tokenCalls *int32, sent *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			atomic.AddInt32(tokenCalls, 1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(r.Form.Get("assertion"), ".") != 2 {
				http.Error(w, "bad grant", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "expires_in": 3600})
		case strings.HasSuffix(r.URL.Path, "/messages:send"):
			if r.Header.Get("Authorization") != "Bearer at-1" {
				http.Error(w, "unauthenticated", 401)
				return
			}
			var body map[string]map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			msg := body["message"]
			if msg["token"] == "gone" {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`))
				return
			}
			*sent = append(*sent, msg)
			_, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeKeyFile(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048) // throwaway key for this test only
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	raw, _ := json.Marshal(map[string]string{
		"client_email": "piyo-push@test.iam.gserviceaccount.com", "private_key": string(pemKey), "token_uri": tokenURI,
	})
	p := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFCMSendWithServiceAccount(t *testing.T) {
	var tokenCalls int32
	var sent []map[string]any
	srv := fakeGoogle(t, &tokenCalls, &sent)
	defer srv.Close()

	s, err := New("piyo-test", writeKeyFile(t, srv.URL+"/token"))
	if err != nil {
		t.Fatal(err)
	}
	f := s.(*FCM)
	f.url = srv.URL + "/v1/projects/piyo-test/messages:send"
	ctx := context.Background()

	if err := f.Send(ctx, Message{Token: "tok-1", Data: map[string]string{"type": "contribution_accepted"}, High: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.Send(ctx, Message{Topic: "dataset", Data: map[string]string{"type": "dataset", "version": "16"}}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0]["token"] != "tok-1" || sent[1]["topic"] != "dataset" {
		t.Fatalf("sent: %v", sent)
	}
	if a := sent[0]["android"].(map[string]any); a["priority"] != "high" {
		t.Fatalf("priority: %v", a)
	}
	if tokenCalls != 1 {
		t.Fatalf("access token should be cached, fetched %d times", tokenCalls)
	}
	if err := f.Send(ctx, Message{Token: "gone"}); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("want ErrUnregistered, got %v", err)
	}
	if err := f.Send(ctx, Message{}); err == nil {
		t.Fatal("message without token or topic must fail")
	}
}

func TestMetadataSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" || !strings.Contains(r.URL.RawQuery, "firebase.messaging") {
			http.Error(w, "bad", 403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "md-1", "expires_in": 3599})
	}))
	defer srv.Close()
	m := &metadataSource{url: srv.URL}
	tok, err := m.token(context.Background(), srv.Client())
	if err != nil || tok != "md-1" {
		t.Fatalf("token %q err %v", tok, err)
	}
}
