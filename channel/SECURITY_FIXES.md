# Security Fixes Required Before Deployment

## Critical Issues

### 1. Signature Verification Bypass (feishu.go:56-60)

**Current Code:**
```go
if f.cfg.EncryptKey == "" {
    // No encryption configured — skip signature check.
    return nil
}
```

**Fix:** Require EncryptKey or add loud warning:
```go
if f.cfg.EncryptKey == "" {
    log.Println("[SECURITY WARNING] Feishu webhook verification disabled - EncryptKey not configured")
    log.Println("[SECURITY WARNING] This should ONLY be used in development environments")
    return nil
}
```

Better: Fail fast on startup if EncryptKey is empty in production.

### 2. PKCS7 Padding Bounds Check (crypto.go:50-65)

**Fix:** Add bounds check before slicing:
```go
func pkcs7Unpad(data []byte) ([]byte, error) {
    if len(data) == 0 {
        return nil, errors.New("feishu: empty plaintext")
    }
    pad := int(data[len(data)-1])
    if pad == 0 || pad > aes.BlockSize || pad > len(data) {  // ADD: || pad > len(data)
        return nil, errors.New("feishu: invalid PKCS7 padding")
    }
    // ... rest unchanged
}
```

### 3. Unbounded Goroutine Spawning (server.go:77-81)

**Fix:** Add worker pool with bounded concurrency:
```go
type ChannelServer struct {
    channels       map[string]Channel
    sessionManager *SessionManager
    mux            *http.ServeMux
    workerPool     chan struct{} // semaphore
}

func NewChannelServer(mgr *SessionManager) *ChannelServer {
    s := &ChannelServer{
        channels:       make(map[string]Channel),
        sessionManager: mgr,
        mux:            http.NewServeMux(),
        workerPool:     make(chan struct{}, 100), // max 100 concurrent
    }
    s.mux.HandleFunc("/health", s.handleHealth)
    return s
}

// In makeWebhookHandler:
go func() {
    s.workerPool <- struct{}{}        // acquire
    defer func() { <-s.workerPool }() // release
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    s.sessionManager.Handle(ctx, ch, msg)
}()
```

## High Priority Issues

### 4. Challenge Response Handling (feishu.go:117-120)

**Problem:** Response written to r.Body instead of http.ResponseWriter.

**Fix:** Return special marker and handle in server:
```go
// In feishu.go ParseMessage:
if err := json.Unmarshal(body, &challenge); err == nil && challenge.Challenge != "" {
    return &channel.IncomingMessage{
        Content: "__CHALLENGE__:" + challenge.Challenge,
    }, nil
}

// In server.go makeWebhookHandler (after ParseMessage):
if msg != nil && strings.HasPrefix(msg.Content, "__CHALLENGE__:") {
    challenge := strings.TrimPrefix(msg.Content, "__CHALLENGE__:")
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"challenge": challenge})
    return
}
```

### 5. Session Eviction Goroutine Leak (session_manager.go:80-97)

**Fix:** Add Close() method with stop channel:
```go
type SessionManager struct {
    agent    *cc.Agent
    sessions sync.Map
    config   SessionConfig
    stopCh   chan struct{}
}

func NewSessionManager(agent *cc.Agent, cfg SessionConfig) *SessionManager {
    // ... existing code ...
    m := &SessionManager{
        agent:  agent,
        config: cfg,
        stopCh: make(chan struct{}),
    }
    go m.evictLoop()
    return m
}

func (m *SessionManager) Close() {
    close(m.stopCh)
}

func (m *SessionManager) evictLoop() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            // ... eviction logic ...
        case <-m.stopCh:
            return
        }
    }
}
```

### 6. Input Validation (session_manager.go:69)

**Fix:** Add validation before processing:
```go
// Validate message content
if len(msg.Content) == 0 {
    _ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: Empty message]"})
    return
}
if len(msg.Content) > 10000 {
    _ = ch.SendMessage(ctx, msg.Sender, OutgoingMessage{Content: "[Error: Message too long]"})
    return
}

result, err := us.session.Run(ctx, msg.Content)
```

## Medium Priority

- Add error logging for SendMessage failures (currently silently ignored)
- Reduce HTTP client timeout from 15s to 10s
- Add per-user rate limiting
- Use VerifyToken field or remove it
- Add timestamp parsing error handling

## Testing Required

- [ ] Unit tests for crypto.go (decrypt, pkcs7Unpad)
- [ ] Unit tests for signature verification
- [ ] Unit tests for session management concurrency
- [ ] Integration test for webhook flow
- [ ] Load test for goroutine pool
- [ ] Test challenge response handling

## Deployment Checklist

- [ ] Fix all Critical issues
- [ ] Fix all High Priority issues
- [ ] Add unit tests
- [ ] Add integration tests
- [ ] Configure monitoring/metrics
- [ ] Set up rate limiting
- [ ] Review production configuration (EncryptKey must be set)
