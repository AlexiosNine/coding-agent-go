package feishu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexioschen/cc-connect/goagent/channel"
)

const (
	sendMessageURL = "https://open.feishu.cn/open-apis/im/v1/messages"
	maxMsgLen      = 4096
	sigWindow      = 5 * time.Minute
)

// Config holds the credentials and settings for a Feishu bot.
type Config struct {
	AppID       string
	AppSecret   string
	VerifyToken string
	EncryptKey  string // empty = no encryption
}

// FeishuChannel implements channel.Channel for the Feishu (Lark) platform.
type FeishuChannel struct {
	cfg    Config
	tokens *TokenCache
	client *http.Client
}

// New creates a FeishuChannel with the given configuration.
func New(cfg Config) *FeishuChannel {
	if cfg.EncryptKey == "" {
		log.Println("[SECURITY WARNING] FeishuChannel: EncryptKey not set — webhook signature verification is DISABLED")
	}
	return &FeishuChannel{
		cfg:    cfg,
		tokens: newTokenCache(cfg.AppID, cfg.AppSecret),
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Name returns "feishu".
func (f *FeishuChannel) Name() string { return "feishu" }

// VerifyWebhook validates the request signature using HMAC-SHA256.
// Feishu signs requests with: sha256(timestamp + "\n" + nonce + "\n" + encryptKey + "\n" + body)
// and places the result in the X-Lark-Signature header.
// It also enforces a 5-minute timestamp window to prevent replay attacks.
func (f *FeishuChannel) VerifyWebhook(r *http.Request) error {
	if f.cfg.EncryptKey == "" {
		// SECURITY WARNING: Running without signature verification!
		// This should ONLY be used in development environments.
		log.Println("[SECURITY WARNING] Feishu webhook verification disabled - EncryptKey not configured")
		log.Println("[SECURITY WARNING] Anyone can send forged webhooks to this server")
		return nil
	}

	timestamp := r.Header.Get("X-Lark-Request-Timestamp")
	nonce := r.Header.Get("X-Lark-Request-Nonce")
	sig := r.Header.Get("X-Lark-Signature")

	// Replay protection: reject requests older than 5 minutes.
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("feishu: invalid timestamp header")
	}
	if time.Since(time.Unix(ts, 0)).Abs() > sigWindow {
		return fmt.Errorf("feishu: timestamp outside 5-minute window")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	// Restore body for subsequent reads.
	r.Body = io.NopCloser(bytes.NewReader(body))

	mac := hmac.New(sha256.New, []byte(f.cfg.EncryptKey))
	fmt.Fprintf(mac, "%s\n%s\n%s\n%s", timestamp, nonce, f.cfg.EncryptKey, string(body))
	expected := fmt.Sprintf("%x", mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return fmt.Errorf("feishu: signature mismatch")
	}
	return nil
}

// ParseMessage decodes the webhook body into an IncomingMessage.
// Returns (nil, nil) for url_verification challenge events (response already written).
func (f *FeishuChannel) ParseMessage(r *http.Request) (*channel.IncomingMessage, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Handle encrypted payload.
	if f.cfg.EncryptKey != "" {
		var enc EncryptedEvent
		if err := json.Unmarshal(body, &enc); err != nil {
			return nil, err
		}
		if enc.Encrypt != "" {
			body, err = decrypt(f.cfg.EncryptKey, enc.Encrypt)
			if err != nil {
				return nil, fmt.Errorf("feishu: decrypt: %w", err)
			}
		}
	}

	// URL verification challenge (first-time webhook setup).
	// Return a special marker so the server can write the JSON response synchronously.
	var challenge ChallengeRequest
	if err := json.Unmarshal(body, &challenge); err == nil && challenge.Challenge != "" {
		return &channel.IncomingMessage{Content: "__CHALLENGE__:" + challenge.Challenge}, nil
	}

	// Normal event envelope.
	var raw struct {
		Header EventHeader     `json:"header"`
		Event  json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	if raw.Header.EventType != "im.message.receive_v1" {
		return nil, nil // ignore other event types
	}

	var ev MessageReceiveEvent
	if err := json.Unmarshal(raw.Event, &ev); err != nil {
		return nil, err
	}

	// Only handle text messages.
	if ev.Message.MessageType != "text" {
		return nil, nil
	}

	var tc TextContent
	if err := json.Unmarshal([]byte(ev.Message.Content), &tc); err != nil {
		return nil, err
	}

	// Strip @mentions from group messages.
	text := strings.TrimSpace(tc.Text)

	ts, _ := strconv.ParseInt(ev.Message.CreateTime, 10, 64)
	return &channel.IncomingMessage{
		ID:    ev.Message.MessageID,
		ChatID: ev.Message.ChatID,
		Content: text,
		Timestamp: time.UnixMilli(ts),
		Sender: channel.Target{
			Platform: "feishu",
			Type:     chatType(ev.Message.ChatType),
			ID:       ev.Sender.SenderID.OpenID,
		},
	}, nil
}

// SendMessage posts a text message to Feishu.
// Messages longer than maxMsgLen are split into multiple sends.
func (f *FeishuChannel) SendMessage(ctx context.Context, target channel.Target, msg channel.OutgoingMessage) error {
	receiveIDType := "open_id"
	if target.Type == "group" {
		receiveIDType = "chat_id"
	}

	chunks := splitMessage(msg.Content, maxMsgLen)
	for _, chunk := range chunks {
		if err := f.sendChunk(ctx, target.ID, receiveIDType, chunk); err != nil {
			return err
		}
	}
	return nil
}

// sendChunk sends a single text chunk to the Feishu API.
func (f *FeishuChannel) sendChunk(ctx context.Context, receiveID, receiveIDType, text string) error {
	token, err := f.tokens.Get(ctx)
	if err != nil {
		return err
	}

	content, _ := json.Marshal(TextContent{Text: text})
	reqBody, _ := json.Marshal(SendMessageRequest{
		ReceiveID: receiveID,
		MsgType:   "text",
		Content:   string(content),
	})

	url := fmt.Sprintf("%s?receive_id_type=%s", sendMessageURL, receiveIDType)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result SendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("feishu: send message error %d: %s", result.Code, result.Msg)
	}
	return nil
}

// chatType maps Feishu chat_type to the canonical "user"/"group" vocabulary.
func chatType(t string) string {
	if t == "p2p" {
		return "user"
	}
	return "group"
}

// splitMessage splits text into chunks of at most maxLen runes.
func splitMessage(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(runes) > 0 {
		end := min(maxLen, len(runes))
		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}
