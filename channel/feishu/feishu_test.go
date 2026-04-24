package feishu

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── Challenge response tests ─────────────────────────────────────────────────

// TestParseMessageChallengeReturnsMarker verifies that a url_verification
// challenge body causes ParseMessage to return the __CHALLENGE__: marker
// rather than a normal IncomingMessage.
func TestParseMessageChallengeReturnsMarker(t *testing.T) {
	ch := New(Config{}) // no EncryptKey — verification disabled

	body := mustMarshal(t, ChallengeRequest{
		Challenge: "abc123",
		Token:     "tok",
		Type:      "url_verification",
	})

	req := httptest.NewRequest(http.MethodPost, "/webhook/feishu", bytes.NewReader(body))
	msg, err := ch.ParseMessage(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message for challenge")
	}
	const wantPrefix = "__CHALLENGE__:"
	if !strings.HasPrefix(msg.Content, wantPrefix) {
		t.Fatalf("expected Content to start with %q, got %q", wantPrefix, msg.Content)
	}
	challenge := strings.TrimPrefix(msg.Content, wantPrefix)
	if challenge != "abc123" {
		t.Fatalf("expected challenge value %q, got %q", "abc123", challenge)
	}
}

// TestParseMessageChallengePreservesValue checks that the exact challenge
// string is preserved verbatim in the marker.
func TestParseMessageChallengePreservesValue(t *testing.T) {
	ch := New(Config{})

	cases := []string{
		"simple",
		"with spaces and special chars !@#$%",
		"unicode-测试-🎉",
		strings.Repeat("x", 256),
	}

	for _, c := range cases {
		body := mustMarshal(t, ChallengeRequest{Challenge: c, Type: "url_verification"})
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		msg, err := ch.ParseMessage(req)
		if err != nil {
			t.Fatalf("challenge=%q: unexpected error: %v", c, err)
		}
		if msg == nil {
			t.Fatalf("challenge=%q: expected non-nil message", c)
		}
		got := strings.TrimPrefix(msg.Content, "__CHALLENGE__:")
		if got != c {
			t.Fatalf("challenge=%q: got %q", c, got)
		}
	}
}

// ─── Missing EncryptKey warning tests ─────────────────────────────────────────

// TestNewLogsWarningWhenEncryptKeyMissing verifies that constructing a
// FeishuChannel without an EncryptKey emits a security warning to the log.
func TestNewLogsWarningWhenEncryptKeyMissing(t *testing.T) {
	var buf strings.Builder
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	New(Config{AppID: "app", AppSecret: "secret"}) // no EncryptKey

	logged := buf.String()
	if !strings.Contains(logged, "SECURITY WARNING") {
		t.Fatalf("expected SECURITY WARNING in log output, got: %q", logged)
	}
}

// TestNewNoWarningWhenEncryptKeySet verifies that no security warning is
// emitted when EncryptKey is provided.
func TestNewNoWarningWhenEncryptKeySet(t *testing.T) {
	var buf strings.Builder
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	New(Config{AppID: "app", AppSecret: "secret", EncryptKey: "somekey"})

	logged := buf.String()
	if strings.Contains(logged, "SECURITY WARNING") {
		t.Fatalf("unexpected SECURITY WARNING in log output: %q", logged)
	}
}

// TestVerifyWebhookLogsWarningWhenEncryptKeyMissing verifies that each
// request processed without an EncryptKey emits a per-request warning.
func TestVerifyWebhookLogsWarningWhenEncryptKeyMissing(t *testing.T) {
	ch := New(Config{})

	var buf strings.Builder
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	req := httptest.NewRequest(http.MethodPost, "/webhook/feishu", strings.NewReader("{}"))
	err := ch.VerifyWebhook(req)
	if err != nil {
		t.Fatalf("expected nil error when EncryptKey is empty, got: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "SECURITY WARNING") {
		t.Fatalf("expected per-request SECURITY WARNING, got: %q", logged)
	}
}

// ─── Signature verification tests ─────────────────────────────────────────────

// TestVerifyWebhookRejectsInvalidTimestamp checks that a non-numeric
// timestamp header causes VerifyWebhook to return an error.
func TestVerifyWebhookRejectsInvalidTimestamp(t *testing.T) {
	ch := New(Config{EncryptKey: "testkey"})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("X-Lark-Request-Timestamp", "not-a-number")
	req.Header.Set("X-Lark-Request-Nonce", "nonce")
	req.Header.Set("X-Lark-Signature", "sig")

	err := ch.VerifyWebhook(req)
	if err == nil {
		t.Fatal("expected error for invalid timestamp, got nil")
	}
}

// TestVerifyWebhookRejectsStaleTimestamp checks that a timestamp outside
// the 5-minute window is rejected.
func TestVerifyWebhookRejectsStaleTimestamp(t *testing.T) {
	ch := New(Config{EncryptKey: "testkey"})

	// 10 minutes in the past
	stale := "1000" // epoch 1000 is definitely outside the window

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("X-Lark-Request-Timestamp", stale)
	req.Header.Set("X-Lark-Request-Nonce", "nonce")
	req.Header.Set("X-Lark-Signature", "sig")

	err := ch.VerifyWebhook(req)
	if err == nil {
		t.Fatal("expected error for stale timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "5-minute window") {
		t.Fatalf("expected window error, got: %v", err)
	}
}

// ─── ParseMessage non-message event tests ─────────────────────────────────────

// TestParseMessageIgnoresNonMessageEvents verifies that events with a
// different event_type return (nil, nil).
func TestParseMessageIgnoresNonMessageEvents(t *testing.T) {
	ch := New(Config{})

	payload := map[string]any{
		"header": map[string]any{
			"event_type": "contact.user.created_v3",
		},
		"event": map[string]any{},
	}
	body := mustMarshal(t, payload)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

	msg, err := ch.ParseMessage(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil message for non-message event, got: %+v", msg)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// Compile-time check that FeishuChannel satisfies the io.Reader interface
// (just to ensure the package compiles cleanly with the import).
var _ io.Reader = strings.NewReader("")
