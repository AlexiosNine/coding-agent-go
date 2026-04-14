package cc

import (
	"context"
	"errors"
	"math"
	"time"
)

// RetryConfig controls automatic retry behavior for provider calls.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts. Default 3.
	MaxRetries int
	// InitDelay is the initial delay before the first retry. Default 1s.
	InitDelay time.Duration
	// MaxDelay is the maximum delay between retries. Default 30s.
	MaxDelay time.Duration
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		InitDelay:  1 * time.Second,
		MaxDelay:   30 * time.Second,
	}
}

// retry executes fn with exponential backoff for retryable errors.
// It returns immediately for non-retryable errors.
func retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for attempt := range cfg.MaxRetries + 1 {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}
		if attempt == cfg.MaxRetries {
			break
		}

		delay := cfg.delay(attempt)

		var pe *ProviderError
		if errors.As(lastErr, &pe) && pe.RetryAfter > delay {
			delay = pe.RetryAfter
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

// delay calculates the backoff delay for a given attempt.
func (cfg RetryConfig) delay(attempt int) time.Duration {
	d := time.Duration(float64(cfg.InitDelay) * math.Pow(2, float64(attempt)))
	if d > cfg.MaxDelay {
		d = cfg.MaxDelay
	}
	return d
}
