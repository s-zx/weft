// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package httpretry wraps http.Client.Do with retry on transient failures
// (network errors, 429, 500/502/503/504). Used by aiusechat backends so a
// flaky provider/network does not abort an in-progress chat.
//
// Retries happen ONLY before the SSE stream starts: once a non-retryable
// status comes back, control returns to the caller and the stream is
// consumed normally. A mid-stream error must NOT be retried by the caller —
// it would re-emit content already delivered to the client.
package httpretry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	DefaultMaxRetries     = 3
	DefaultInitialBackoff = 500 * time.Millisecond
	DefaultMaxBackoff     = 30 * time.Second
	DefaultMultiplier     = 2.0
)

type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
}

func DefaultConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     DefaultMaxRetries,
		InitialBackoff: DefaultInitialBackoff,
		MaxBackoff:     DefaultMaxBackoff,
		Multiplier:     DefaultMultiplier,
	}
}

// Do executes req with retries on transient failures.
//
// On the first call, the request body (if any) is fully read and buffered so
// it can be replayed on subsequent attempts; the original req must not be
// reused after this call. Headers are captured once and re-cloned per attempt.
//
// Returns the response from the first non-retryable attempt, or — when
// retries are exhausted — the most recent response (for status-based failures)
// or the most recent error (for transport-level failures).
func Do(ctx context.Context, client *http.Client, req *http.Request, cfg RetryConfig, label string) (*http.Response, error) {
	cfg = normalize(cfg)

	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	method := req.Method
	url := req.URL.String()
	headers := req.Header.Clone()

	build := func(ctx context.Context) (*http.Request, error) {
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		out, err := http.NewRequestWithContext(ctx, method, url, r)
		if err != nil {
			return nil, err
		}
		out.Header = headers.Clone()
		return out, nil
	}

	var lastResp *http.Response
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			wait := backoffDuration(attempt, cfg, lastResp)
			if lastResp != nil {
				_, _ = io.Copy(io.Discard, lastResp.Body)
				_ = lastResp.Body.Close()
				lastResp = nil
			}
			log.Printf("httpretry %s: retry %d/%d in %s\n", label, attempt, cfg.MaxRetries, wait.Round(time.Millisecond))
			if err := sleepCtx(ctx, wait); err != nil {
				return nil, err
			}
		}

		attemptReq, err := build(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(attemptReq)
		if err != nil {
			if attempt >= cfg.MaxRetries || !isRetryableError(err) {
				return nil, err
			}
			continue
		}
		if !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt >= cfg.MaxRetries {
			return resp, nil
		}
		lastResp = resp
	}
}

func normalize(c RetryConfig) RetryConfig {
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = DefaultInitialBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = DefaultMaxBackoff
	}
	if c.Multiplier < 1.0 {
		c.Multiplier = DefaultMultiplier
	}
	return c
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// backoffDuration computes the wait before attempt number `attempt` (1-indexed
// among retries — attempt=1 is the first retry). Honors Retry-After when the
// previous response carried it; otherwise uses exponential backoff with equal
// jitter (lower half deterministic, upper half random) to avoid thundering
// herd while keeping the floor non-zero.
func backoffDuration(attempt int, cfg RetryConfig, prev *http.Response) time.Duration {
	if prev != nil {
		if d, ok := parseRetryAfter(prev.Header.Get("Retry-After")); ok {
			if d < 0 {
				d = 0
			}
			if d > cfg.MaxBackoff {
				d = cfg.MaxBackoff
			}
			return d
		}
	}
	base := float64(cfg.InitialBackoff) * math.Pow(cfg.Multiplier, float64(attempt-1))
	if base > float64(cfg.MaxBackoff) {
		base = float64(cfg.MaxBackoff)
	}
	half := base / 2
	return time.Duration(half + rand.Float64()*half)
}

func parseRetryAfter(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second, true
	}
	if t, err := http.ParseTime(value); err == nil {
		return time.Until(t), true
	}
	return 0, false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
