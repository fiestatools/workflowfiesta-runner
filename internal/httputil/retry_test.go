package httputil_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workflowfiesta-runner/internal/httputil"
)

func makeResp(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestDo(t *testing.T) {
	tests := []struct {
		name      string
		cfg       httputil.RetryConfig
		responses []struct {
			status int // 0 means nil response (network error)
			err    error
		}
		wantAttempts int
		wantErr      bool
	}{
		{
			name: "success on first attempt",
			cfg:  httputil.DefaultConfig(),
			responses: []struct {
				status int
				err    error
			}{
				{status: 200},
			},
			wantAttempts: 1,
			wantErr:      false,
		},
		{
			name: "success after two transient failures",
			cfg: httputil.RetryConfig{
				MaxAttempts: 3,
				BaseDelay:   1 * time.Millisecond,
				MaxDelay:    5 * time.Millisecond,
			},
			responses: []struct {
				status int
				err    error
			}{
				{err: errors.New("connection refused")},
				{status: 503},
				{status: 200},
			},
			wantAttempts: 3,
			wantErr:      false,
		},
		{
			name: "exhausts all attempts",
			cfg: httputil.RetryConfig{
				MaxAttempts: 3,
				BaseDelay:   1 * time.Millisecond,
				MaxDelay:    5 * time.Millisecond,
			},
			responses: []struct {
				status int
				err    error
			}{
				{err: errors.New("timeout")},
				{err: errors.New("timeout")},
				{err: errors.New("timeout")},
			},
			wantAttempts: 3,
			wantErr:      true,
		},
		{
			name: "non-retryable error returns immediately",
			cfg:  httputil.DefaultConfig(),
			responses: []struct {
				status int
				err    error
			}{
				{status: 400},
			},
			wantAttempts: 1,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			fn := func() (*http.Response, error) {
				idx := int(attempts.Add(1)) - 1
				if idx >= len(tt.responses) {
					t.Fatalf("unexpected call: attempt %d", idx)
				}
				r := tt.responses[idx]
				if r.status > 0 {
					return makeResp(r.status), r.err
				}
				return nil, r.err
			}

			resp, err := httputil.Do(context.Background(), tt.cfg, fn)

			if got := int(attempts.Load()); got != tt.wantAttempts {
				t.Errorf("attempts = %d, want %d", got, tt.wantAttempts)
			}
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	cfg := httputil.RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    1 * time.Second,
	}

	var attempts atomic.Int32
	fn := func() (*http.Response, error) {
		attempts.Add(1)
		// Cancel after first attempt so the retry sleep gets interrupted.
		cancel()
		return nil, errors.New("connection refused")
	}

	resp, err := httputil.Do(ctx, cfg, fn)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in error chain, got: %v", err)
	}
	if got := int(attempts.Load()); got != 1 {
		t.Errorf("attempts = %d, want 1 (should not retry after cancel)", got)
	}
}

func TestDo_OnRetryCallback(t *testing.T) {
	cfg := httputil.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	}

	var retryCount int
	cfg.OnRetry = func(attempt int, delay time.Duration, err error) {
		retryCount++
		if delay <= 0 {
			t.Errorf("expected positive delay, got %v", delay)
		}
	}

	fn := func() (*http.Response, error) {
		return nil, errors.New("fail")
	}

	resp, _ := httputil.Do(context.Background(), cfg, fn)
	if resp != nil {
		resp.Body.Close()
	}

	// 3 attempts = 2 retries (no retry after the last attempt)
	if retryCount != 2 {
		t.Errorf("OnRetry called %d times, want 2", retryCount)
	}
}

func TestDefaultIsRetryable(t *testing.T) {
	tests := []struct {
		name   string
		status int // 0 means nil response (network error)
		err    error
		want   bool
	}{
		{"network error", 0, errors.New("dial tcp"), true},
		{"429", 429, nil, true},
		{"502", 502, nil, true},
		{"503", 503, nil, true},
		{"504", 504, nil, true},
		{"200", 200, nil, false},
		{"400", 400, nil, false},
		{"401", 401, nil, false},
		{"404", 404, nil, false},
		{"500", 500, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp *http.Response
			if tt.status > 0 {
				resp = makeResp(tt.status)
				defer resp.Body.Close()
			}
			got := httputil.DefaultIsRetryable(resp, tt.err)
			if got != tt.want {
				t.Errorf("DefaultIsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}
