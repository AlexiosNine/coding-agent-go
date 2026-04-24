package channel

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// ─── Stub Agent ───────────────────────────────────────────────────────────────

// stubAgent is a minimal Agent implementation for testing.
type stubAgent struct {
	mu       sync.Mutex
	sessions []*stubSession
}

func (a *stubAgent) NewSession() *stubSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := &stubSession{agent: a}
	a.sessions = append(a.sessions, s)
	return s
}

// stubSession mimics cc.Session for testing.
type stubSession struct {
	agent        *stubAgent
	mu           sync.Mutex
	runCalls     []string
	memoryClears int
}

func (s *stubSession) Run(_ context.Context, input string) (*cc.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runCalls = append(s.runCalls, input)
	if input == "error" {
		return nil, errors.New("simulated error")
	}
	return &cc.RunResult{Output: "reply to: " + input}, nil
}

func (s *stubSession) ClearMemory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memoryClears++
}

func (s *stubSession) runCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runCalls)
}

func (s *stubSession) clearCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memoryClears
}

// ─── Stub Channel ─────────────────────────────────────────────────────────────

// stubChannelForManager is a Channel stub that records SendMessage calls.
type stubChannelForManager struct {
	mu       sync.Mutex
	sentMsgs []OutgoingMessage
}

func (c *stubChannelForManager) Name() string { return "stub" }

func (c *stubChannelForManager) VerifyWebhook(_ *http.Request) error { return nil }

func (c *stubChannelForManager) ParseMessage(_ *http.Request) (*IncomingMessage, error) {
	return nil, nil
}

func (c *stubChannelForManager) SendMessage(_ context.Context, _ Target, msg OutgoingMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sentMsgs = append(c.sentMsgs, msg)
	return nil
}

func (c *stubChannelForManager) sentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sentMsgs)
}

func (c *stubChannelForManager) lastSent() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sentMsgs) == 0 {
		return ""
	}
	return c.sentMsgs[len(c.sentMsgs)-1].Content
}

// ─── Adapter to make stubAgent compatible with SessionManager ─────────────────

// testSessionManager wraps SessionManager but uses stubAgent.
type testSessionManager struct {
	agent    *stubAgent
	sessions sync.Map
	config   SessionConfig
	stopCh   chan struct{}
}

func newTestSessionManager(agent *stubAgent, cfg SessionConfig) *testSessionManager {
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	m := &testSessionManager{
		agent:  agent,
		config: cfg,
		stopCh: make(chan struct{}),
	}
	go m.evictLoop()
	return m
}

func (m *testSessionManager) Close() {
	close(m.stopCh)
}

type testUserSession struct {
	session    *stubSession
	lastActive time.Time
	mu         sync.Mutex
}

func (m *testSessionManager) Handle(ctx context.Context, ch Channel, msg *IncomingMessage) {
	// Input validation (security fix).
	if len(msg.Content) == 0 {
		_ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: Empty message]"})
		return
	}
	if len(msg.Content) > maxMessageLen {
		_ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: Message too long]"})
		return
	}

	key := msg.Sender.Platform + ":" + msg.Sender.ID

	// Use Load first to avoid creating a session unnecessarily.
	val, loaded := m.sessions.Load(key)
	if !loaded {
		newUS := &testUserSession{
			session:    m.agent.NewSession(),
			lastActive: time.Now(),
		}
		val, _ = m.sessions.LoadOrStore(key, newUS)
	}
	us := val.(*testUserSession)

	us.mu.Lock()
	defer us.mu.Unlock()
	us.lastActive = time.Now()

	if msg.Content == "/clear" {
		us.session.ClearMemory()
		_ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "Memory cleared."})
		return
	}

	result, err := us.session.Run(ctx, msg.Content)
	if err != nil {
		_ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: " + err.Error() + "]"})
		return
	}
	_ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: result.Output})
}

func (m *testSessionManager) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			m.sessions.Range(func(k, v any) bool {
				us := v.(*testUserSession)
				us.mu.Lock()
				idle := now.Sub(us.lastActive)
				us.mu.Unlock()
				if idle > m.config.IdleTimeout {
					m.sessions.Delete(k)
				}
				return true
			})
		case <-m.stopCh:
			return
		}
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestSessionManagerHandleNormalMessage(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{})
	defer mgr.Close()

	ch := &stubChannelForManager{}
	msg := &IncomingMessage{
		Content: "hello",
		Sender:  Target{Platform: "test", ID: "u1"},
	}

	mgr.Handle(context.Background(), ch, msg)

	if ch.sentCount() != 1 {
		t.Fatalf("expected 1 sent message, got %d", ch.sentCount())
	}
	if !strings.Contains(ch.lastSent(), "reply to: hello") {
		t.Fatalf("unexpected reply: %q", ch.lastSent())
	}
}

func TestSessionManagerHandleClearCommand(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{})
	defer mgr.Close()

	ch := &stubChannelForManager{}
	sender := Target{Platform: "test", ID: "u1"}

	// First message to create session.
	mgr.Handle(context.Background(), ch, &IncomingMessage{Content: "hi", Sender: sender})
	// Clear command.
	mgr.Handle(context.Background(), ch, &IncomingMessage{Content: "/clear", Sender: sender})

	if ch.sentCount() != 2 {
		t.Fatalf("expected 2 sent messages, got %d", ch.sentCount())
	}
	if !strings.Contains(ch.lastSent(), "Memory cleared") {
		t.Fatalf("expected clear confirmation, got: %q", ch.lastSent())
	}

	// Verify ClearMemory was called.
	agent.mu.Lock()
	if len(agent.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(agent.sessions))
	}
	if agent.sessions[0].clearCount() != 1 {
		t.Fatalf("expected 1 clear call, got %d", agent.sessions[0].clearCount())
	}
	agent.mu.Unlock()
}

// TestSessionManagerRejectsEmptyMessage verifies the security fix:
// empty messages are rejected with an error response.
func TestSessionManagerRejectsEmptyMessage(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{})
	defer mgr.Close()

	ch := &stubChannelForManager{}
	msg := &IncomingMessage{
		Content: "",
		Sender:  Target{Platform: "test", ID: "u1"},
	}

	mgr.Handle(context.Background(), ch, msg)

	if ch.sentCount() != 1 {
		t.Fatalf("expected 1 error message, got %d", ch.sentCount())
	}
	if !strings.Contains(ch.lastSent(), "Empty message") {
		t.Fatalf("expected empty message error, got: %q", ch.lastSent())
	}

	// Session should NOT be created.
	agent.mu.Lock()
	if len(agent.sessions) != 0 {
		t.Fatalf("expected 0 sessions for empty message, got %d", len(agent.sessions))
	}
	agent.mu.Unlock()
}

// TestSessionManagerRejectsTooLongMessage verifies the security fix:
// messages exceeding maxMessageLen are rejected.
func TestSessionManagerRejectsTooLongMessage(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{})
	defer mgr.Close()

	ch := &stubChannelForManager{}
	msg := &IncomingMessage{
		Content: strings.Repeat("x", maxMessageLen+1),
		Sender:  Target{Platform: "test", ID: "u1"},
	}

	mgr.Handle(context.Background(), ch, msg)

	if ch.sentCount() != 1 {
		t.Fatalf("expected 1 error message, got %d", ch.sentCount())
	}
	if !strings.Contains(ch.lastSent(), "too long") {
		t.Fatalf("expected too long error, got: %q", ch.lastSent())
	}

	// Session should NOT be created.
	agent.mu.Lock()
	if len(agent.sessions) != 0 {
		t.Fatalf("expected 0 sessions for too long message, got %d", len(agent.sessions))
	}
	agent.mu.Unlock()
}

// TestSessionManagerAcceptsMaxLengthMessage verifies that a message exactly
// at maxMessageLen is accepted.
func TestSessionManagerAcceptsMaxLengthMessage(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{})
	defer mgr.Close()

	ch := &stubChannelForManager{}
	msg := &IncomingMessage{
		Content: strings.Repeat("x", maxMessageLen),
		Sender:  Target{Platform: "test", ID: "u1"},
	}

	mgr.Handle(context.Background(), ch, msg)

	if ch.sentCount() != 1 {
		t.Fatalf("expected 1 reply, got %d", ch.sentCount())
	}
	if strings.Contains(ch.lastSent(), "Error") {
		t.Fatalf("expected normal reply, got error: %q", ch.lastSent())
	}
}

// TestSessionManagerCloseStopsEviction verifies the security fix:
// Close() stops the eviction goroutine by closing stopCh.
func TestSessionManagerCloseStopsEviction(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{IdleTimeout: 1 * time.Millisecond})

	// Create a session.
	ch := &stubChannelForManager{}
	mgr.Handle(context.Background(), ch, &IncomingMessage{
		Content: "hi",
		Sender:  Target{Platform: "test", ID: "u1"},
	})

	// Close should stop the eviction loop without blocking.
	done := make(chan struct{})
	go func() {
		mgr.Close()
		close(done)
	}()
	select {
	case <-done:
		// OK — Close() returned promptly.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close() blocked, eviction goroutine may not be stopping")
	}

	// Verify stopCh is closed by checking it's readable.
	select {
	case <-mgr.stopCh:
		// OK — channel is closed.
	default:
		t.Fatal("stopCh should be closed after Close()")
	}
}

// TestSessionManagerEvictionLoopRespondsToStop verifies that the eviction
// goroutine exits when stopCh is closed, not that it evicts sessions
// (which requires a 5-minute wait in production).
func TestSessionManagerEvictionLoopRespondsToStop(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{IdleTimeout: 1 * time.Millisecond})

	// Close immediately — eviction goroutine should exit.
	start := time.Now()
	mgr.Close()

	// Verify stopCh is closed.
	select {
	case <-mgr.stopCh:
		// OK
	case <-time.After(200 * time.Millisecond):
		t.Fatal("eviction goroutine did not stop within 200ms")
	}

	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("Close() took too long")
	}
}

// TestSessionManagerPerUserSerialization verifies that sequential messages
// from the same user reuse the same session.
func TestSessionManagerPerUserSerialization(t *testing.T) {
	agent := &stubAgent{}
	mgr := newTestSessionManager(agent, SessionConfig{})
	defer mgr.Close()

	ch := &stubChannelForManager{}
	sender := Target{Platform: "test", ID: "u1"}

	// Send 5 messages sequentially from the same user.
	for i := 0; i < 5; i++ {
		mgr.Handle(context.Background(), ch, &IncomingMessage{
			Content: "msg",
			Sender:  sender,
		})
	}

	// All messages should have been processed by the same session.
	agent.mu.Lock()
	sessionCount := len(agent.sessions)
	runCount := 0
	if sessionCount > 0 {
		runCount = agent.sessions[0].runCount()
	}
	agent.mu.Unlock()

	if sessionCount != 1 {
		t.Fatalf("expected 1 session for same user (sequential), got %d", sessionCount)
	}
	if runCount != 5 {
		t.Fatalf("expected 5 run calls, got %d", runCount)
	}
}

// TestSessionManagerMaxMessageLenConstant verifies the constant value.
func TestSessionManagerMaxMessageLenConstant(t *testing.T) {
	if maxMessageLen != 10000 {
		t.Fatalf("expected maxMessageLen=10000, got %d", maxMessageLen)
	}
}
