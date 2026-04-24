package channel

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

const maxMessageLen = 10000

// SessionConfig controls session lifecycle behavior.
type SessionConfig struct {
	// IdleTimeout is how long a session can be inactive before being evicted.
	// Default: 30 minutes.
	IdleTimeout time.Duration
}

// SessionManager manages per-user Agent sessions across IM platforms.
// Each user gets an isolated cc.Session; messages from the same user are
// processed serially to avoid context interleaving.
type SessionManager struct {
	agent    *cc.Agent
	sessions sync.Map // key: "platform:user_id" → *userSession
	config   SessionConfig
	stopCh   chan struct{}
}

// userSession wraps a cc.Session with per-user serialization and activity tracking.
type userSession struct {
	session    *cc.Session
	lastActive time.Time
	mu         sync.Mutex
}

// NewSessionManager creates a SessionManager and starts the background eviction loop.
func NewSessionManager(agent *cc.Agent, cfg SessionConfig) *SessionManager {
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	m := &SessionManager{
		agent:  agent,
		config: cfg,
		stopCh: make(chan struct{}),
	}
	go m.evictLoop()
	return m
}

// Close stops the background eviction goroutine.
func (m *SessionManager) Close() {
	close(m.stopCh)
}

// Handle processes an incoming message: finds or creates the user's session,
// runs the agent, and sends the reply via the channel.
// It is safe to call concurrently; messages from the same user are serialized.
func (m *SessionManager) Handle(ctx context.Context, ch Channel, msg *IncomingMessage) {
	// Validate message content before processing.
	if len(msg.Content) == 0 {
		if err := ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: Empty message]"}); err != nil {
			log.Printf("[session] send error to %s:%s: %v", msg.Sender.Platform, msg.Sender.ID, err)
		}
		return
	}
	if len(msg.Content) > maxMessageLen {
		if err := ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: Message too long]"}); err != nil {
			log.Printf("[session] send error to %s:%s: %v", msg.Sender.Platform, msg.Sender.ID, err)
		}
		return
	}

	key := fmt.Sprintf("%s:%s", msg.Sender.Platform, msg.Sender.ID)

	val, _ := m.sessions.LoadOrStore(key, &userSession{
		session:    m.agent.NewSession(),
		lastActive: time.Now(),
	})
	us := val.(*userSession)

	us.mu.Lock()
	defer us.mu.Unlock()

	us.lastActive = time.Now()

	// Handle /clear command.
	if msg.Content == "/clear" {
		us.session.ClearMemory()
		if err := ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "Memory cleared."}); err != nil {
			log.Printf("[session] send error to %s:%s: %v", msg.Sender.Platform, msg.Sender.ID, err)
		}
		return
	}

	result, err := us.session.Run(ctx, msg.Content)
	if err != nil {
		if sendErr := ch.SendMessage(ctx, msg.Sender, OutgoingMessage{
			Content: fmt.Sprintf("[Error: %s]", err.Error()),
		}); sendErr != nil {
			log.Printf("[session] send error to %s:%s: %v", msg.Sender.Platform, msg.Sender.ID, sendErr)
		}
		return
	}

	if err := ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: result.Output}); err != nil {
		log.Printf("[session] send error to %s:%s: %v", msg.Sender.Platform, msg.Sender.ID, err)
	}
}

// evictLoop periodically removes sessions that have been idle too long.
func (m *SessionManager) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			m.sessions.Range(func(k, v any) bool {
				us := v.(*userSession)
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
