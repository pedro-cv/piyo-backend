// Package push sends data-only notifications to the Android app through
// Firebase Cloud Messaging (HTTP v1 API). Messages carry a type and an id or
// version, never user text: the app owns the Spanish copy (strings.xml).
//
// Credentials, in order:
//  1. FCM_CREDENTIALS_FILE: a service-account JSON key (VPS, local tests);
//  2. otherwise the GCP metadata server (Cloud Run, GCE), so on Cloud Run no
//     key file exists at all: the service account needs the
//     "Firebase Cloud Messaging API Admin" role.
//
// Only the standard library is used (JWT signing with crypto/rsa).
// Privacy: tokens are never logged; error messages from FCM are truncated.
package push

import (
	"context"
	"errors"
)

// Message is one data message for a single device (Token) or every
// subscriber of a topic (Topic). Exactly one of the two must be set.
type Message struct {
	Token string
	Topic string
	Data  map[string]string
	// High asks FCM to wake the device now (device-targeted review results);
	// topic broadcasts use normal priority.
	High bool
}

// ErrUnregistered means the token is no longer valid (app uninstalled or
// token rotated); the caller should delete it.
var ErrUnregistered = errors.New("push: token unregistered")

// Sender delivers messages. Implementations must be goroutine-safe.
type Sender interface {
	Send(ctx context.Context, m Message) error
	Enabled() bool
}

// Noop is the sender used when FCM_PROJECT_ID is empty: push is disabled and
// every Send succeeds without doing anything.
type Noop struct{}

func (Noop) Send(context.Context, Message) error { return nil }
func (Noop) Enabled() bool                       { return false }

// New returns an FCM sender for projectID, or Noop when projectID is empty.
func New(projectID, credentialsFile string) (Sender, error) {
	if projectID == "" {
		return Noop{}, nil
	}
	var ts tokenSource
	if credentialsFile != "" {
		sa, err := loadServiceAccount(credentialsFile)
		if err != nil {
			return nil, err
		}
		ts = sa
	} else {
		ts = &metadataSource{}
	}
	return newFCM(projectID, ts), nil
}
