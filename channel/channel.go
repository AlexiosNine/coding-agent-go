// Package channel provides IM platform integration for the cc-connect agent.
// It defines the Channel interface and common types used by all platform implementations.
package channel

import (
	"context"
	"net/http"
	"time"
)

// Channel is the interface that all IM platform implementations must satisfy.
type Channel interface {
	// Name returns the platform identifier (e.g., "feishu", "qq").
	Name() string

	// VerifyWebhook validates the incoming request signature.
	// Returns an error if the request is invalid or forged.
	VerifyWebhook(r *http.Request) error

	// ParseMessage extracts a standardized IncomingMessage from the request.
	// Returns nil, nil for non-message events (e.g., url_verification challenge).
	ParseMessage(r *http.Request) (*IncomingMessage, error)

	// SendMessage delivers a message to the specified target.
	SendMessage(ctx context.Context, target Target, msg OutgoingMessage) error
}

// IncomingMessage is a platform-agnostic representation of a received message.
type IncomingMessage struct {
	ID        string
	Sender    Target
	ChatID    string
	Content   string
	Timestamp time.Time
}

// OutgoingMessage is the content to send back to the user.
type OutgoingMessage struct {
	Content string
}

// Target identifies a message recipient or sender on a specific platform.
type Target struct {
	Platform string // "feishu" / "qq"
	Type     string // "user" / "group"
	ID       string
}
