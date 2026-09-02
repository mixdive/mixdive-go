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
	"time"
)

// transport is the HTTP layer beneath Client: it posts rendered payloads to
// the Mixdive ingest API with the configured retry policy. Everything here
// runs on the client's dispatcher goroutine — the caller-facing methods only
// ever enqueue, which is what keeps the host application out of the blast
// radius of a slow or unreachable server.
type transport struct {
	serverUrl  string
	apiKey     string
	appVersion string
	httpClient *http.Client
	retries    int
}

// APIError is a non-2xx response from the server, handed to the client's
// error handler when delivery fails for good.
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

// trackPayloads routes converted items. One item that is a plain event goes
// to the endpoint built for it: there is nothing for it to be related to,
// and older servers know it. Everything else travels as a track envelope.
func (t *transport) trackPayloads(ctx context.Context, payloads []itemPayload) error {
	if len(payloads) == 1 && payloads[0].Event != "" {
		return t.post(ctx, "/ingest/event", eventPayload{payloads[0], payloads[0].Event})
	}
	return t.post(ctx, "/ingest/track", struct {
		Items []itemPayload `json:"items"`
	}{payloads})
}

// post sends payload to an ingest path, retrying per the configured policy
// with doubling backoff (200 ms start).
func (t *transport) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mixdive: encode payload: %w", err)
	}
	backoff := 200 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err = t.doOnce(ctx, path, body)
		if err == nil || attempt >= t.retries || !retryable(err) {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
	}
}

func (t *transport) doOnce(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.serverUrl+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mixdive: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", t.apiKey)
	if t.appVersion != "" {
		req.Header.Set("X-App-Version", t.appVersion)
	}
	res, err := t.httpClient.Do(req)
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
