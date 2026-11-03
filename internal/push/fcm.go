package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const fcmEndpoint = "https://fcm.googleapis.com/v1/projects/%s/messages:send"

// FCM sends through the HTTP v1 API.
type FCM struct {
	url    string
	tokens tokenSource
	client *http.Client
}

func newFCM(projectID string, ts tokenSource) *FCM {
	return &FCM{
		url:    fmt.Sprintf(fcmEndpoint, projectID),
		tokens: ts,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *FCM) Enabled() bool { return true }

type fcmAndroid struct {
	Priority string `json:"priority"`
	TTL      string `json:"ttl"`
}

type fcmMessage struct {
	Token   string            `json:"token,omitempty"`
	Topic   string            `json:"topic,omitempty"`
	Data    map[string]string `json:"data"`
	Android fcmAndroid        `json:"android"`
}

// Send posts one message. A 404 UNREGISTERED answer maps to ErrUnregistered.
func (f *FCM) Send(ctx context.Context, m Message) error {
	if (m.Token == "") == (m.Topic == "") {
		return errors.New("push: exactly one of token or topic is required")
	}
	access, err := f.tokens.token(ctx, f.client)
	if err != nil {
		return fmt.Errorf("push: access token: %w", err)
	}
	prio := "normal"
	if m.High {
		prio = "high"
	}
	body, _ := json.Marshal(map[string]fcmMessage{"message": {
		Token: m.Token, Topic: m.Topic, Data: m.Data,
		// A review result or release older than a day is not worth a notification.
		Android: fcmAndroid{Priority: prio, TTL: "86400s"},
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("push: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusNotFound || strings.Contains(string(raw), "UNREGISTERED") {
		return ErrUnregistered
	}
	msg := string(raw)
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("push: fcm status %d: %s", resp.StatusCode, msg)
}
