package channel

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

const maxConcurrentWorkers = 100

// ChannelServer is an HTTP server that routes incoming webhooks to the
// appropriate Channel implementation and dispatches agent processing.
type ChannelServer struct {
	channels       map[string]Channel
	sessionManager *SessionManager
	mux            *http.ServeMux
	workerPool     chan struct{} // semaphore limiting concurrent agent runs
}

// NewChannelServer creates a ChannelServer backed by the given SessionManager.
func NewChannelServer(mgr *SessionManager) *ChannelServer {
	s := &ChannelServer{
		channels:       make(map[string]Channel),
		sessionManager: mgr,
		mux:            http.NewServeMux(),
		workerPool:     make(chan struct{}, maxConcurrentWorkers),
	}
	s.mux.HandleFunc("/health", s.handleHealth)
	return s
}

// Register adds a Channel implementation and wires its webhook route.
func (s *ChannelServer) Register(ch Channel) {
	s.channels[ch.Name()] = ch
	s.mux.HandleFunc("/webhook/"+ch.Name(), s.makeWebhookHandler(ch))
}

// ServeHTTP implements http.Handler so ChannelServer can be passed to http.ListenAndServe.
func (s *ChannelServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *ChannelServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *ChannelServer) makeWebhookHandler(ch Channel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Verify signature before doing anything else.
		if err := ch.VerifyWebhook(r); err != nil {
			log.Printf("[channel/%s] webhook verification failed: %v", ch.Name(), err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		msg, err := ch.ParseMessage(r)
		if err != nil {
			log.Printf("[channel/%s] parse error: %v", ch.Name(), err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Handle URL verification challenge — must respond synchronously.
		if msg != nil && strings.HasPrefix(msg.Content, "__CHALLENGE__:") {
			challenge := strings.TrimPrefix(msg.Content, "__CHALLENGE__:")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": challenge})
			return
		}

		// Respond immediately — Feishu requires a reply within 3 seconds.
		w.WriteHeader(http.StatusOK)

		if msg == nil {
			return
		}

		// Check worker pool capacity before spawning goroutine.
		select {
		case s.workerPool <- struct{}{}:
		default:
			log.Printf("[channel/%s] worker pool full, dropping message from %s", ch.Name(), msg.Sender.ID)
			return
		}

		go func() {
			defer func() { <-s.workerPool }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			s.sessionManager.Handle(ctx, ch, msg)
		}()
	}
}
