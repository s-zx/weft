// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
	"golang.org/x/net/html"
)

const (
	WebFetchTimeout      = 15 * time.Second
	WebFetchMaxBytes     = 512 * 1024
	WebFetchMaxOutput    = 100 * 1024
	WebFetchMaxRedirects = 5
)

var errBlockedAddress = errors.New("blocked address (private/loopback/link-local/multicast not allowed)")

type webFetchInput struct {
	URL string `json:"url"`
}

func WebFetch(approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "web_fetch",
		DisplayName: "Fetch Web Page",
		Description: "Fetch a URL and return the text content. Useful for reading documentation, checking APIs, or retrieving web page content. Returns extracted text (HTML tags stripped). Maximum 100KB of text returned.",
		ToolLogName: "agent:web_fetch",
		Prompt: `web_fetch: Fetches a URL and returns its text (HTML stripped).
- Must be http:// or https://. Private/loopback/link-local addresses are blocked (SSRF guard).
- Output is capped at 100KB. For very long pages, fetch and then immediately summarize — don't quote the whole body back to the user.
- Use for documentation, API references, release notes. NOT a generic "search the web" — you must already know the URL.
- If the user gave you a link, fetch it before guessing. Don't paraphrase from training data when the source is one fetch away.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch (must start with http:// or https://).",
				},
			},
			"required":             []string{"url"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, output any, _ *uctypes.UIMessageDataToolUse) string {
			parsed, err := parseWebFetchInput(input)
			if err != nil {
				return fmt.Sprintf("web_fetch (invalid: %v)", err)
			}
			if output != nil {
				return fmt.Sprintf("fetched %s", truncURL(parsed.URL))
			}
			return fmt.Sprintf("fetching %s", truncURL(parsed.URL))
		},
		ToolTextCallback: func(input any) (string, error) {
			parsed, err := parseWebFetchInput(input)
			if err != nil {
				return "", err
			}
			return fetchAndExtract(parsed.URL)
		},
		ToolApproval: approval,
	}
}

func parseWebFetchInput(input any) (*webFetchInput, error) {
	params := &webFetchInput{}
	if input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if err := utilfn.ReUnmarshal(params, input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	params.URL = strings.TrimSpace(params.URL)
	if params.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if err := validateFetchURL(params.URL); err != nil {
		return nil, err
	}
	return params, nil
}

// validateFetchURL enforces http(s) scheme, no userinfo, and a non-empty
// hostname. IP-literal hosts are rejected here if they resolve to a blocked
// range; DNS hostnames are checked at dial time (TOCTOU-safe).
func validateFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must use http or https scheme")
	}
	if u.User != nil {
		return fmt.Errorf("url must not contain userinfo")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url must have a host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return errBlockedAddress
		}
	}
	return nil
}

// isBlockedIP rejects loopback, link-local, multicast, unspecified, and
// RFC1918 private ranges (plus IPv4-mapped variants).
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// IPv4-mapped IPv6 (e.g. ::ffff:127.0.0.1) — re-check the v4 form.
	if v4 := ip.To4(); v4 != nil && !v4.Equal(ip) {
		if v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsLinkLocalMulticast() ||
			v4.IsMulticast() || v4.IsUnspecified() || v4.IsPrivate() {
			return true
		}
	}
	// AWS/GCP/Azure IMDS (169.254.169.254) is already covered by IsLinkLocalUnicast,
	// but be explicit for clarity / future ranges.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return true
	}
	return false
}

// safeDialContext wraps a net.Dialer so each connection re-resolves the host
// and rejects any address that lands in a blocked range. Closes the SSRF
// TOCTOU gap (DNS rebinding / public-resolves-to-private).
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	resolver := net.DefaultResolver
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("%w: %s -> %s", errBlockedAddress, host, ip)
		}
	}
	// Dial the first non-blocked address directly to avoid a second lookup
	// that could return a different (rebinding) result.
	var lastErr error
	for _, ip := range ips {
		conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no addresses to dial for %s", host)
	}
	return nil, lastErr
}

func makeWebFetchClient() *http.Client {
	transport := &http.Transport{
		DialContext:           safeDialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= WebFetchMaxRedirects {
				return fmt.Errorf("too many redirects (>%d)", WebFetchMaxRedirects)
			}
			if err := validateFetchURL(req.URL.String()); err != nil {
				return fmt.Errorf("blocked redirect: %w", err)
			}
			return nil
		},
	}
}

func fetchAndExtract(rawURL string) (string, error) {
	if err := validateFetchURL(rawURL); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), WebFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("User-Agent", "Crest/1.0 (coding agent)")
	req.Header.Set("Accept", "text/html, text/plain, application/json, */*")

	client := makeWebFetchClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, WebFetchMaxBytes))
	if err != nil {
		return "", fmt.Errorf("read body failed: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		text := extractText(string(body))
		return utilfn.TruncateString(text, WebFetchMaxOutput), nil
	}
	return utilfn.TruncateString(string(body), WebFetchMaxOutput), nil
}

func extractText(rawHTML string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(rawHTML))
	var sb strings.Builder
	skip := false
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(sb.String())
		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" || tag == "svg" {
				skip = true
			}
			if tag == "br" || tag == "p" || tag == "div" || tag == "li" || tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4" || tag == "h5" || tag == "h6" || tag == "tr" {
				sb.WriteByte('\n')
			}
		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" || tag == "svg" {
				skip = false
			}
		case html.TextToken:
			if !skip {
				text := strings.TrimSpace(tokenizer.Token().Data)
				if text != "" {
					if sb.Len() > 0 {
						sb.WriteByte(' ')
					}
					sb.WriteString(text)
				}
			}
		}
	}
}

func truncURL(rawURL string) string {
	if len(rawURL) > 60 {
		return rawURL[:57] + "..."
	}
	return rawURL
}
