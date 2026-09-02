package mixdive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// capture records every request body the test server receives.
type capture struct {
	mu     sync.Mutex
	bodies []map[string]any
	keys   []string // X-Api-Key per request
	paths  []string // request path per request
}

func (c *capture) handler(status int, response string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		c.bodies = append(c.bodies, body)
		c.keys = append(c.keys, r.Header.Get("X-Api-Key"))
		c.paths = append(c.paths, r.URL.Path)
		c.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}
}

// errCollector is a WithErrorHandler sink tests inspect after flushing.
type errCollector struct {
	mu   sync.Mutex
	errs []error
}

func (ec *errCollector) add(err error) {
	ec.mu.Lock()
	ec.errs = append(ec.errs, err)
	ec.mu.Unlock()
}

func (ec *errCollector) all() []error {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return append([]error(nil), ec.errs...)
}

// flushed waits until everything enqueued so far has been delivered —
// sending is fire-and-forget, so tests flush before asserting on captures.
func flushed(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func TestTrackSendsContractPayload(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL+"/", "mx_testkey", // trailing slash must not break the URL
		WithErrorHandler(func(err error) { t.Errorf("unexpected error: %v", err) }))
	ts := time.Date(2026, 8, 5, 14, 3, 22, 0, time.UTC)
	client.Track(NewEvent("checkout_completed").
		SetEventUser("user-1").
		SetTimestamp(ts).
		SetPropertyString("plan", "team"))
	flushed(t, client)

	if len(cap.bodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(cap.bodies))
	}
	got := cap.bodies[0]
	if got["event_key"] != "checkout_completed" || got["user_id"] != "user-1" {
		t.Errorf("wrong payload: %v", got)
	}
	if got["timestamp"] != "2026-08-05T14:03:22Z" {
		t.Errorf("wrong timestamp: %v", got["timestamp"])
	}
	if id, _ := got["id"].(string); id == "" {
		t.Error("id was not auto-generated")
	}
	if cap.keys[0] != "mx_testkey" {
		t.Errorf("wrong X-Api-Key: %q", cap.keys[0])
	}
}

func TestTrackRetriesReuseItemId(t *testing.T) {
	cap := &capture{}
	attempts := 0
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			cap.handler(http.StatusServiceUnavailable, `{"error":"queue unavailable"}`)(w, r)
			return
		}
		cap.handler(http.StatusAccepted, `{"queued":true}`)(w, r)
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "mx_testkey", WithRetries(2),
		WithErrorHandler(func(err error) { t.Errorf("unexpected error: %v", err) }))
	client.Track(NewEvent("app_opened"))
	flushed(t, client)

	if len(cap.bodies) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(cap.bodies))
	}
	first, second := cap.bodies[0]["id"], cap.bodies[1]["id"]
	if first == "" || first != second {
		t.Errorf("retry must reuse the item id: %v vs %v", first, second)
	}
}

func TestTrackBatchEnvelope(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/batch", cap.handler(http.StatusAccepted, `{"queued":2}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "mx_testkey",
		WithErrorHandler(func(err error) { t.Errorf("unexpected error: %v", err) }))
	client.TrackBatch(Items([]*Event{NewEvent("a"), NewEvent("b")})...)
	client.TrackBatch()    // no items is a no-op
	client.TrackBatch(nil) // and so is an untyped nil item
	flushed(t, client)

	if len(cap.bodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(cap.bodies))
	}
	items, ok := cap.bodies[0]["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected items array of 2, got %v", cap.bodies[0])
	}
	for i, raw := range items {
		e := raw.(map[string]any)
		if id, _ := e["id"].(string); id == "" {
			t.Errorf("item %d missing id", i)
		}
		if e["event"] == nil {
			t.Errorf("item %d missing event key", i)
		}
	}
}

func TestSetUserOmitsEmptyFields(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/user", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "mx_testkey",
		WithErrorHandler(func(err error) { t.Errorf("unexpected error: %v", err) }))
	client.SetUser(SetUser("user-1").
		SetName("Ada Lovelace").
		SetDataString("plan", "team"))
	flushed(t, client)

	got := cap.bodies[0]
	if got["user_id"] != "user-1" || got["name"] != "Ada Lovelace" {
		t.Errorf("wrong payload: %v", got)
	}
	for _, absent := range []string{"username", "email"} {
		if _, present := got[absent]; present {
			t.Errorf("empty field %q must be omitted (merge semantics)", absent)
		}
	}
}

func TestPermanentAPIErrorIsReportedNotRetried(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusUnauthorized, `{"error":"unknown or revoked API key"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ec := &errCollector{}
	client := New(srv.URL, "mx_wrong", WithRetries(3), WithErrorHandler(ec.add))
	client.Track(NewEvent("app_opened"))
	flushed(t, client)

	if len(cap.bodies) != 1 {
		t.Errorf("401 must not be retried; got %d attempts", len(cap.bodies))
	}
	errs := ec.all()
	if len(errs) != 1 {
		t.Fatalf("expected 1 reported error, got %d: %v", len(errs), errs)
	}
	var apiErr *APIError
	if !errors.As(errs[0], &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected a 401 APIError, got %v", errs[0])
	}
	if apiErr.Message != "unknown or revoked API key" {
		t.Errorf("wrong message: %q", apiErr.Message)
	}
}

// Invalid items are dropped at the call — reported synchronously to the
// error handler, before anything could leave the process.
func TestInvalidItemsReportedAtTheCall(t *testing.T) {
	ec := &errCollector{}
	client := New("http://127.0.0.1:1", "mx_testkey", WithRetries(0), WithErrorHandler(ec.add))
	client.Track(NewEvent(""))
	client.SetUser(SetUser(""))
	errs := ec.all()
	if len(errs) != 2 {
		t.Fatalf("expected 2 reported drops, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], errNoEventKey) {
		t.Errorf("expected errNoEventKey, got %v", errs[0])
	}
	if !errors.Is(errs[1], errNoUserId) {
		t.Errorf("expected errNoUserId, got %v", errs[1])
	}
}
