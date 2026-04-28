// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package httpretry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fastConfig(maxRetries int) RetryConfig {
	return RetryConfig{
		MaxRetries:     maxRetries,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		Multiplier:     2.0,
	}
}

func newReq(t *testing.T, method, url string, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

func TestDo_SuccessFirstAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), fastConfig(3), "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestDo_RetryOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), fastConfig(3), "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestDo_RetryOn5xx(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt32(&calls, 1)
				if n < 2 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(200)
			}))
			defer srv.Close()

			resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), fastConfig(3), "test")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("got %d", resp.StatusCode)
			}
			if got := atomic.LoadInt32(&calls); got != 2 {
				t.Fatalf("calls = %d, want 2", got)
			}
		})
	}
}

func TestDo_NoRetryOn4xx(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), fastConfig(3), "test")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != status {
				t.Fatalf("got %d, want %d", resp.StatusCode, status)
			}
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Fatalf("calls = %d, want 1 (no retry)", got)
			}
		})
	}
}

func TestDo_ExhaustRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), fastConfig(2), "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("got %d, want 503", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", got)
	}
}

func TestDo_BodyReplayed(t *testing.T) {
	var calls int32
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), srv.Client(), newReq(t, "POST", srv.URL, "hello-world"), fastConfig(3), "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("got %d bodies, want 3", len(bodies))
	}
	for i, b := range bodies {
		if b != "hello-world" {
			t.Fatalf("body[%d] = %q, want %q", i, b, "hello-world")
		}
	}
}

func TestDo_HeadersReplayed(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("X-Test-Token"))
		mu.Unlock()
		if len(seen) < 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	req := newReq(t, "GET", srv.URL, "")
	req.Header.Set("X-Test-Token", "t-abc")
	resp, err := Do(context.Background(), srv.Client(), req, fastConfig(3), "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(seen))
	}
	for i, s := range seen {
		if s != "t-abc" {
			t.Fatalf("attempt %d: header = %q, want t-abc", i, s)
		}
	}
}

func TestDo_RespectsRetryAfter(t *testing.T) {
	var calls int32
	var firstAttempt, secondAttempt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			firstAttempt = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		secondAttempt = time.Now()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := fastConfig(3)
	cfg.MaxBackoff = 5 * time.Second
	resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	elapsed := secondAttempt.Sub(firstAttempt)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("Retry-After not honored: elapsed=%s, want >= ~1s", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Retry-After exceeded budget: elapsed=%s", elapsed)
	}
}

func TestDo_RetryAfterCappedByMaxBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer srv.Close()

	cfg := fastConfig(1)
	cfg.MaxBackoff = 30 * time.Millisecond
	start := time.Now()
	resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), cfg, "test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if elapsed > 200*time.Millisecond {
		t.Fatalf("MaxBackoff did not cap Retry-After: elapsed=%s", elapsed)
	}
}

func TestDo_CtxCanceledDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	cfg := RetryConfig{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		Multiplier:     2.0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Do(ctx, srv.Client(), newReq(t, "GET", srv.URL, ""), cfg, "test")
	if err == nil {
		t.Fatal("expected error from cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDo_TransportErrorRetried(t *testing.T) {
	var calls int32
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		_ = n
		w.WriteHeader(200)
	}))
	srvURL = srv.URL
	srv.Close()

	cfg := fastConfig(2)
	_, err := Do(context.Background(), &http.Client{Timeout: 200 * time.Millisecond}, newReq(t, "GET", srvURL, ""), cfg, "test")
	if err == nil {
		t.Fatal("expected transport error after closed server")
	}
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("expected url.Error, got %T: %v", err, err)
	}
}

func TestDo_DefaultConfigSane(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxRetries != DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, DefaultMaxRetries)
	}
	if cfg.InitialBackoff != DefaultInitialBackoff {
		t.Errorf("InitialBackoff = %s, want %s", cfg.InitialBackoff, DefaultInitialBackoff)
	}
	if cfg.MaxBackoff != DefaultMaxBackoff {
		t.Errorf("MaxBackoff = %s, want %s", cfg.MaxBackoff, DefaultMaxBackoff)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		ok   bool
		want time.Duration
	}{
		{"", false, 0},
		{"5", true, 5 * time.Second},
		{"0", true, 0},
		{"abc", false, 0},
	}
	for _, tt := range tests {
		got, ok := parseRetryAfter(tt.in)
		if ok != tt.ok {
			t.Errorf("parseRetryAfter(%q): ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestIsRetryableStatus(t *testing.T) {
	yes := []int{429, 500, 502, 503, 504}
	no := []int{200, 201, 204, 301, 302, 400, 401, 403, 404, 422, 501}
	for _, s := range yes {
		if !isRetryableStatus(s) {
			t.Errorf("isRetryableStatus(%d) = false, want true", s)
		}
	}
	for _, s := range no {
		if isRetryableStatus(s) {
			t.Errorf("isRetryableStatus(%d) = true, want false", s)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	if isRetryableError(nil) {
		t.Error("nil should not be retryable")
	}
	if isRetryableError(context.Canceled) {
		t.Error("context.Canceled should not be retryable")
	}
	if isRetryableError(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should not be retryable")
	}
	if !isRetryableError(io.ErrUnexpectedEOF) {
		t.Error("io.ErrUnexpectedEOF should be retryable")
	}
}

func TestNormalizeClampsNegativeAndZero(t *testing.T) {
	in := RetryConfig{
		MaxRetries:     -3,
		InitialBackoff: 0,
		MaxBackoff:     -1,
		Multiplier:     0.5,
	}
	got := normalize(in)
	if got.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0", got.MaxRetries)
	}
	if got.InitialBackoff != DefaultInitialBackoff {
		t.Errorf("InitialBackoff = %s, want default", got.InitialBackoff)
	}
	if got.MaxBackoff != DefaultMaxBackoff {
		t.Errorf("MaxBackoff = %s, want default", got.MaxBackoff)
	}
	if got.Multiplier != DefaultMultiplier {
		t.Errorf("Multiplier = %v, want default", got.Multiplier)
	}
}

func TestDo_ZeroRetriesIsSingleAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	cfg := fastConfig(0)
	resp, err := Do(context.Background(), srv.Client(), newReq(t, "GET", srv.URL, ""), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 (zero retries)", got)
	}
}
