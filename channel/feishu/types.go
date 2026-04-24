// Package feishu implements the channel.Channel interface for the Feishu (Lark) platform.
package feishu

// ─── Webhook event envelope ───────────────────────────────────────────────────

// EventEnvelope is the top-level structure for all Feishu webhook events.
type EventEnvelope struct {
	Schema string      `json:"schema"`
	Header EventHeader `json:"header"`
	Event  any `json:"event"` // decoded separately per event type
}

// EventHeader contains metadata common to all events.
type EventHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

// ─── URL verification ─────────────────────────────────────────────────────────

// ChallengeRequest is sent by Feishu when first configuring a webhook URL.
type ChallengeRequest struct {
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Type      string `json:"type"`
}

// ChallengeResponse must be returned verbatim to pass the handshake.
type ChallengeResponse struct {
	Challenge string `json:"challenge"`
}

// ─── Encrypted event wrapper ──────────────────────────────────────────────────

// EncryptedEvent wraps an AES-encrypted payload.
type EncryptedEvent struct {
	Encrypt string `json:"encrypt"`
}

// ─── im.message.receive_v1 ────────────────────────────────────────────────────

// MessageReceiveEvent is the payload for im.message.receive_v1 events.
type MessageReceiveEvent struct {
	Sender  MessageSender  `json:"sender"`
	Message MessageContent `json:"message"`
}

// MessageSender identifies who sent the message.
type MessageSender struct {
	SenderID   SenderID `json:"sender_id"`
	SenderType string   `json:"sender_type"`
	TenantKey  string   `json:"tenant_key"`
}

// SenderID holds the various ID formats for a Feishu user.
type SenderID struct {
	UnionID string `json:"union_id"`
	UserID  string `json:"user_id"`
	OpenID  string `json:"open_id"`
}

// MessageContent holds the message body.
type MessageContent struct {
	MessageID   string `json:"message_id"`
	RootID      string `json:"root_id"`
	ParentID    string `json:"parent_id"`
	CreateTime  string `json:"create_time"`
	ChatID      string `json:"chat_id"`
	ChatType    string `json:"chat_type"` // "p2p" or "group"
	MessageType string `json:"message_type"`
	Content     string `json:"content"` // JSON-encoded, e.g. {"text":"hello"}
}

// TextContent is the decoded form of a text message's Content field.
type TextContent struct {
	Text string `json:"text"`
}

// ─── Send message API ─────────────────────────────────────────────────────────

// SendMessageRequest is the body for POST /open-apis/im/v1/messages.
type SendMessageRequest struct {
	ReceiveID string `json:"receive_id"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"` // JSON-encoded, e.g. {"text":"hello"}
}

// SendMessageResponse is the API response for message sending.
type SendMessageResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
}

// ─── Token API ────────────────────────────────────────────────────────────────

// AppAccessTokenRequest is the body for obtaining an app_access_token.
type AppAccessTokenRequest struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

// AppAccessTokenResponse is the response from the token endpoint.
type AppAccessTokenResponse struct {
	Code           int    `json:"code"`
	Msg            string `json:"msg"`
	AppAccessToken string `json:"app_access_token"`
	Expire         int    `json:"expire"` // seconds
}
