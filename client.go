package mixdive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client sends analytics data to a Mixdive server's ingest API. The server
// acknowledges with 202 and processes asynchronously (fast-ack), so a nil
// error means "queued", with reports following within about a minute.
type Client struct {
	serverUrl  string
	apiKey     string
	appVersion string
	httpClient *http.Client
	retries    int
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client (10 s timeout).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithRetries sets how many times a call is retried after a retryable
// failure — HTTP 503 or a transport error. Default 2; 0 disables retries.
// Retrying is safe: every event carries an id the server deduplicates on,
// and record merges are idempotent by construction.
func WithRetries(n int) Option {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.retries = n
	}
}

// WithAppVersion sets the app version reported in the X-App-Version header
// on every ingest call, which Mixdive stamps on each occurrence and breaks
// reports down by. When this option is not used, the client defaults to the
// host binary's VCS revision (the commit it was built from, when available).
// Pass "" to send no version at all.
func WithAppVersion(v string) Option {
	return func(c *Client) { c.appVersion = v }
}

// New creates a Client. serverUrl is the base URL of your Mixdive server
// (for example "https://analytics.example.com"); apiKey is an app API key
// created under Settings → Apps.
//
// Unless WithAppVersion overrides it, the client reports the host binary's
// VCS revision as the app version — so reports can break usage down by the
// commit that produced it, with zero configuration.
func New(serverUrl, apiKey string, opts ...Option) *Client {
	c := &Client{
		serverUrl:  strings.TrimRight(serverUrl, "/"),
		apiKey:     apiKey,
		appVersion: detectAppVersion(),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		retries:    2,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is a non-2xx response from the server.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mixdive: server returned %d: %s", e.StatusCode, e.Message)
}

// retryable reports whether an attempt may be repeated: queue-unavailable
// responses and transport errors, per the ingest contract. Other API errors
// (401 bad key, 413 too large) are permanent.
func retryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusServiceUnavailable
	}
	return true
}

// post sends payload to an ingest path, retrying per the client's
// configuration with doubling backoff (200 ms start).
func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mixdive: encode payload: %w", err)
	}
	backoff := 200 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err = c.doOnce(ctx, path, body)
		if err == nil || attempt >= c.retries || !retryable(err) {
			return err
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		backoff *= 2
	}
}

func (c *Client) doOnce(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverUrl+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mixdive: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.apiKey)
	if c.appVersion != "" {
		req.Header.Set("X-App-Version", c.appVersion)
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mixdive: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, res.Body) // drain so the connection is reused
		return nil
	}
	var e struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&e)
	return &APIError{StatusCode: res.StatusCode, Message: e.Error}
}

// newItemId returns a random 32-hex-char id, used when an item does not
// carry one of its own. crypto/rand cannot fail on the Go versions this
// module supports.
func newItemId() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
