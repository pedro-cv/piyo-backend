package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// tokenSource returns an OAuth2 access token for the FCM scope.
type tokenSource interface {
	token(ctx context.Context, c *http.Client) (string, error)
}

// cached holds an access token until shortly before it expires.
type cached struct {
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (c *cached) get(fetch func() (string, time.Duration, error)) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != "" && time.Now().Before(c.expires) {
		return c.value, nil
	}
	v, ttl, err := fetch()
	if err != nil {
		return "", err
	}
	c.value, c.expires = v, time.Now().Add(ttl-time.Minute)
	return v, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func readTokenResponse(resp *http.Response) (string, time.Duration, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", 0, fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, raw)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", 0, err
	}
	if tr.AccessToken == "" {
		return "", 0, errors.New("empty access token")
	}
	return tr.AccessToken, time.Duration(tr.ExpiresIn) * time.Second, nil
}

// metadataSource asks the GCP metadata server for the attached service
// account's token (Cloud Run, GCE). No key file is involved.
type metadataSource struct {
	cache cached
	url   string // overridden in tests
}

func (m *metadataSource) token(ctx context.Context, c *http.Client) (string, error) {
	return m.cache.get(func() (string, time.Duration, error) {
		u := m.url
		if u == "" {
			u = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?scopes="+url.QueryEscape(fcmScope), nil)
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("Metadata-Flavor", "Google")
		resp, err := c.Do(req)
		if err != nil {
			return "", 0, fmt.Errorf("metadata server (not on GCP? set FCM_CREDENTIALS_FILE): %w", err)
		}
		return readTokenResponse(resp)
	})
}

// serviceAccount signs a JWT with a service-account key and exchanges it for
// an access token (RFC 7523 grant).
type serviceAccount struct {
	email    string
	tokenURI string
	key      *rsa.PrivateKey
	cache    cached
}

func loadServiceAccount(path string) (*serviceAccount, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("push: read credentials: %w", err)
	}
	var f struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("push: parse credentials: %w", err)
	}
	key, err := parseRSAKey(f.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("push: credentials private_key: %w", err)
	}
	if f.ClientEmail == "" {
		return nil, errors.New("push: credentials without client_email")
	}
	if f.TokenURI == "" {
		f.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &serviceAccount{email: f.ClientEmail, tokenURI: f.TokenURI, key: key}, nil
}

func parseRSAKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("not an RSA key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (s *serviceAccount) assertion(now time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": s.email, "scope": fcmScope, "aud": s.tokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := b64(header) + "." + b64(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + b64(sig), nil
}

func (s *serviceAccount) token(ctx context.Context, c *http.Client) (string, error) {
	return s.cache.get(func() (string, time.Duration, error) {
		jwt, err := s.assertion(time.Now())
		if err != nil {
			return "", 0, err
		}
		form := url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			"assertion":  {jwt},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURI, strings.NewReader(form.Encode()))
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := c.Do(req)
		if err != nil {
			return "", 0, err
		}
		return readTokenResponse(resp)
	})
}
