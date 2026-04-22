package cc

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failingCompactor struct {
	calls int
}

func (f *failingCompactor) Compact(_ context.Context, _ []Message) (string, error) {
	f.calls++
	return "", errors.New("llm unavailable")
}

type okCompactor struct {
	calls int
	label string
}

func (o *okCompactor) Compact(_ context.Context, _ []Message) (string, error) {
	o.calls++
	return o.label + " summary", nil
}

func TestCircuitBreaker_ClosedUsesPrimary(t *testing.T) {
	primary := &okCompactor{label: "llm"}
	fallback := &okCompactor{label: "rule"}
	cb := NewCircuitBreakerCompactor(primary, fallback)

	result, err := cb.Compact(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "llm summary" {
		t.Errorf("expected 'llm summary', got %q", result)
	}
	if primary.calls != 1 || fallback.calls != 0 {
		t.Errorf("expected primary=1 fallback=0, got %d %d", primary.calls, fallback.calls)
	}
}

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	primary := &failingCompactor{}
	fallback := &okCompactor{label: "rule"}
	cb := NewCircuitBreakerCompactor(primary, fallback)

	// 3 failures → open
	for i := 0; i < 3; i++ {
		cb.Compact(context.Background(), nil)
	}

	// Now open: should use fallback directly
	primary.calls = 0
	fallback.calls = 0
	result, _ := cb.Compact(context.Background(), nil)
	if result != "rule summary" {
		t.Errorf("expected fallback, got %q", result)
	}
	if primary.calls != 0 {
		t.Errorf("expected primary not called in open state, got %d", primary.calls)
	}
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	primary := &failingCompactor{}
	fallback := &okCompactor{label: "rule"}
	cb := NewCircuitBreakerCompactor(primary, fallback)
	cb.cooldown = 1 * time.Millisecond // fast cooldown for test

	// Open the circuit
	for i := 0; i < 3; i++ {
		cb.Compact(context.Background(), nil)
	}

	// Wait for cooldown
	time.Sleep(5 * time.Millisecond)

	// Replace primary with working one
	working := &okCompactor{label: "llm-recovered"}
	cb.primary = working

	result, _ := cb.Compact(context.Background(), nil)
	if result != "llm-recovered summary" {
		t.Errorf("expected recovered primary, got %q", result)
	}

	// Should be closed again
	cb.mu.Lock()
	state := cb.state
	cb.mu.Unlock()
	if state != circuitClosed {
		t.Errorf("expected closed after recovery, got %d", state)
	}
}

func TestCircuitBreaker_HalfOpenFailsReopens(t *testing.T) {
	primary := &failingCompactor{}
	fallback := &okCompactor{label: "rule"}
	cb := NewCircuitBreakerCompactor(primary, fallback)
	cb.cooldown = 1 * time.Millisecond

	// Open
	for i := 0; i < 3; i++ {
		cb.Compact(context.Background(), nil)
	}

	time.Sleep(5 * time.Millisecond)

	// HalfOpen probe fails → back to open
	primary.calls = 0
	cb.Compact(context.Background(), nil)

	cb.mu.Lock()
	state := cb.state
	cb.mu.Unlock()
	// failureCount is now 4 (>= maxFailures), so still open
	if state != circuitOpen {
		t.Errorf("expected open after half-open failure, got %d", state)
	}
}
