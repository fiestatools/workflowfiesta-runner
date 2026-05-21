package httputil

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"
)

type RetryConfig struct {
	// MaxAttempts is the total number of attempts (including the first).
	// Default: 3
	MaxAttempts int

	// BaseDelay is the initial backoff delay before the first retry.
	// Default: 500ms
	BaseDelay time.Duration

	// MaxDelay caps the backoff delay.
	// Default: 5s
	MaxDelay time.Duration

	// IsRetryable determines whether a response/error pair should be retried.
	// If nil, DefaultIsRetryable is used.
	IsRetryable func(resp *http.Response, err error) bool

	// OnRetry is called before each retry sleep. Useful for logging.
	// attempt is 0-indexed (0 = first retry, after the initial failure).
	OnRetry func(attempt int, delay time.Duration, err error)
}

func DefaultConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		IsRetryable: DefaultIsRetryable,
	}
}

func DefaultIsRetryable(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// Do executes fn with retries according to cfg.
// fn is called repeatedly until it returns a non-retryable result or MaxAttempts is exhausted.
// The caller is responsible for closing the response body on success.
// On retryable responses, Do closes the body before retrying.
//
// The context controls cancellation between attempts — fn itself should also
// respect ctx if it performs blocking I/O.
func Do(ctx context.Context, cfg RetryConfig, fn func() (*http.Response, error)) (*http.Response, error) {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 5 * time.Second
	}
	isRetryable := cfg.IsRetryable
	if isRetryable == nil {
		isRetryable = DefaultIsRetryable
	}

	var lastErr error
	for attempt := range cfg.MaxAttempts {
		resp, err := fn()
		if !isRetryable(resp, err) {
			return resp, err
		}

		if resp != nil {
			resp.Body.Close()
		}

		lastErr = err
		if lastErr == nil {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		if attempt >= cfg.MaxAttempts-1 {
			break
		}

		delay := backoff(cfg.BaseDelay, cfg.MaxDelay, attempt)

		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt, delay, lastErr)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

func backoff(base, max time.Duration, attempt int) time.Duration {
	delay := base << uint(attempt) // exponential: 500ms, 1s, 2s, ...
	if delay > max {
		delay = max
	}
	jitter := time.Duration(rand.Int64N(int64(delay) / 2))
	return delay/2 + jitter
}
