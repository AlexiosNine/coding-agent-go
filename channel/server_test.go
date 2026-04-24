package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Stub Channel ─────────────────────────────────────────────────────────────

// stubChannel is a minimal Channel implementation for testing.
// VerifyWebhook always succeeds; ParseMessage returns a fixed message.
type stubChannel struct {
	name        string
	parseResult *IncomingMessage
	parseErr    error
	sendCalled  atomic.Int32
}

func (s *stubChannel) Name() string { return s.name }

func (s *stubChannel) VerifyWebhook(_ *http.Request) error { return nil }

func (s *stubChannel) ParseMessage(_ *http.Request) (*IncomingMessage, error) {
	return s.parseResult, s.parseErr
}

func (s *stubChannel) SendMessage(_ context.Context, _ Target, _ OutgoingMessage) error {
	s.sendCalled.Add(1)
	return nil
}

// ─── Stub SessionManager ──────────────────────────────────────────────────────

// stubSessionManager records Handle calls without running a real agent.
type stubSessionManager struct {
	mu      sync.Mutex
	handled []*IncomingMessage
	delay   time.Duration // optional artificial delay per Handle call
}

func newStubSessionManager() *stubSessionManager {
	return &stubSessionManager{}
}

// Handle records the message and optionally sleeps to simulate work.
func (m *stubSessionManager) Handle(_ context.Context, _ Channel, msg *IncomingMessage) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	m.mu.Lock()
	m.handled = append(m.handled, msg)
	m.mu.Unlock()
}

func (m *stubSessionManager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.handled)
}

// ─── ChannelServer constructor helper ─────────────────────────────────────────

// newTestServer builds a ChannelServer wired to a stubSessionManager.
// It replaces the real SessionManager with the stub via a thin wrapper.
func newTestServer(stub *stubSessionManager) *testChannelServer {
	s := &testChannelServer{
		stub:       stub,
		channels:   make(map[string]Channel),
		mux:        http.NewServeMux(),
		workerPool: make(chan struct{}, maxConcurrentWorkers),
	}
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

// testChannelServer mirrors ChannelServer but uses stubSessionManager.
type testChannelServer struct {
	stub       *stubSessionManager
	channels   map[string]Channel
	mux        *http.ServeMux
	workerPool chan struct{}
}

func (s *testChannelServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *testChannelServer) register(ch Channel) {
	s.channels[ch.Name()] = ch
	s.mux.HandleFunc("/webhook/"+ch.Name(), s.makeHandler(ch))
}

func (s *testChannelServer) makeHandler(ch Channel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := ch.VerifyWebhook(r); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		msg, err := ch.ParseMessage(r)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Challenge handling.
		if msg != nil && strings.HasPrefix(msg.Content, "__CHALLENGE__:") {
			challenge := strings.TrimPrefix(msg.Content, "__CHALLENGE__:")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": challenge})
			return
		}
		w.WriteHeader(http.StatusOK)
		if msg == nil {
			return
		}
		// Worker pool semaphore.
		select {
		case s.workerPool <- struct{}{}:
		default:
			return // pool full — drop
		}
		go func() {
			defer func() { <-s.workerPool }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			s.stub.Handle(ctx, ch, msg)
		}()
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer(newStubSessionManager())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Fatalf("expected body 'ok', got %q", w.Body.String())
	}
}

func TestWebhookMethodNotAllowed(t *testing.T) {
	stub := newStubSessionManager()
	srv := newTestServer(stub)
	ch := &stubChannel{name: "test", parseResult: &IncomingMessage{Content: "hi"}}
	srv.register(ch)

	req := httptest.NewRequest(http.MethodGet, "/webhook/test", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestNormalMessageProcessing verifies that a valid POST dispatches to the
// session manager exactly once.
func TestNormalMessageProcessing(t *testing.T) {
	stub := newStubSessionManager()
	srv := newTestServer(stub)
	ch := &stubChannel{
		name:        "test",
		parseResult: &IncomingMessage{Content: "hello", Sender: Target{Platform: "test", ID: "u1"}},
	}
	srv.register(ch)

	req := httptest.NewRequest(http.MethodPost, "/webhook/test", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Give the goroutine time to run.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && stub.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if stub.count() != 1 {
		t.Fatalf("expected 1 handled message, got %d", stub.count())
	}
}

// TestWorkerPoolFull verifies that when the pool is saturated the 101st
// concurrent request is dropped (not queued indefinitely).
func TestWorkerPoolFull(t *testing.T) {
	// Use a slow stub so goroutines stay in-flight long enough to fill the pool.
	stub := &stubSessionManager{delay: 200 * time.Millisecond}
	srv := newTestServer(stub)
	ch := &stubChannel{
		name:        "test",
		parseResult: &IncomingMessage{Content: "msg", Sender: Target{Platform: "test", ID: "u1"}},
	}
	srv.register(ch)

	// Fill the pool with maxConcurrentWorkers requests.
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/webhook/test", strings.NewReader("{}"))
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
		}()
	}

	// Give goroutines time to acquire pool slots.
	time.Sleep(30 * time.Millisecond)

	// The 101st request should be dropped (pool full).
	req := httptest.NewRequest(http.MethodPost, "/webhook/test", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// Server still returns 200 (it responds before dispatching).
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when pool full, got %d", w.Code)
	}

	// Wait for all goroutines to finish.
	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// At most maxConcurrentWorkers messages should have been handled.
	if stub.count() > maxConcurrentWorkers {
		t.Fatalf("expected at most %d handled messages, got %d", maxConcurrentWorkers, stub.count())
	}
}

// TestChallengeResponseHandling verifies that a __CHALLENGE__: message
// causes the server to write a JSON challenge response synchronously.
func TestChallengeResponseHandling(t *testing.T) {
	stub := newStubSessionManager()
	srv := newTestServer(stub)
	ch := &stubChannel{
		name:        "test",
		parseResult: &IncomingMessage{Content: "__CHALLENGE__:xyz789"},
	}
	srv.register(ch)

	req := httptest.NewRequest(http.MethodPost, "/webhook/test", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json Content-Type, got %q", ct)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode challenge response: %v", err)
	}
	if resp["challenge"] != "xyz789" {
		t.Fatalf("expected challenge=xyz789, got %q", resp["challenge"])
	}

	// Challenge must NOT be dispatched to the session manager.
	time.Sleep(50 * time.Millisecond)
	if stub.count() != 0 {
		t.Fatalf("challenge should not be dispatched to session manager, got %d", stub.count())
	}
}

// TestNilMessageNotDispatched verifies that ParseMessage returning (nil, nil)
// does not dispatch to the session manager.
func TestNilMessageNotDispatched(t *testing.T) {
	stub := newStubSessionManager()
	srv := newTestServer(stub)
	ch := &stubChannel{name: "test", parseResult: nil}
	srv.register(ch)

	req := httptest.NewRequest(http.MethodPost, "/webhook/test", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if stub.count() != 0 {
		t.Fatalf("nil message should not be dispatched, got %d", stub.count())
	}
}

// TestWorkerPoolSemaphoreSize verifies the pool capacity constant is 100.
func TestWorkerPoolSemaphoreSize(t *testing.T) {
	if maxConcurrentWorkers != 100 {
		t.Fatalf("expected maxConcurrentWorkers=100, got %d", maxConcurrentWorkers)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// Ensure bytes is used (compile-time check).
var _ = bytes.NewReader
