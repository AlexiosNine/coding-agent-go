package cc

import (
	"context"
	"sync"
	"time"
)

type circuitState int

const (
	circuitClosed   circuitState = iota // normal: use primary
	circuitOpen                         // degraded: use fallback
	circuitHalfOpen                     // probing: try primary once
)

// CircuitBreakerCompactor wraps a primary Compactor (e.g. LLM) with a fallback
// (e.g. rule-based). After maxFailures consecutive primary failures, it switches
// to fallback. After cooldown, it probes the primary once (half-open).
type CircuitBreakerCompactor struct {
	primary  Compactor
	fallback Compactor

	mu           sync.Mutex
	state        circuitState
	failureCount int
	maxFailures  int
	cooldown     time.Duration
	lastFailure  time.Time
}

// NewCircuitBreakerCompactor creates a circuit breaker around primary with fallback.
// Defaults: 3 failures to open, 5 minute cooldown.
func NewCircuitBreakerCompactor(primary, fallback Compactor) *CircuitBreakerCompactor {
	return &CircuitBreakerCompactor{
		primary:     primary,
		fallback:    fallback,
		state:       circuitClosed,
		maxFailures: 3,
		cooldown:    5 * time.Minute,
	}
}

func (cb *CircuitBreakerCompactor) Compact(ctx context.Context, msgs []Message) (string, error) {
	cb.mu.Lock()
	// Transition Open → HalfOpen after cooldown
	if cb.state == circuitOpen && time.Since(cb.lastFailure) > cb.cooldown {
		cb.state = circuitHalfOpen
	}
	state := cb.state
	cb.mu.Unlock()

	if state == circuitOpen {
		return cb.fallback.Compact(ctx, msgs)
	}

	// Closed or HalfOpen: try primary
	result, err := cb.primary.Compact(ctx, msgs)
	if err != nil {
		cb.recordFailure()
		return cb.fallback.Compact(ctx, msgs)
	}

	cb.recordSuccess()
	return result, nil
}

func (cb *CircuitBreakerCompactor) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount++
	cb.lastFailure = time.Now()
	if cb.failureCount >= cb.maxFailures {
		cb.state = circuitOpen
	}
}

func (cb *CircuitBreakerCompactor) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount = 0
	cb.state = circuitClosed
}
