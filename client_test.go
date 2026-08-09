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

func TestTrackSendsContractPayload(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL+"/", "mx_testkey") // trailing slash must not break the URL
	ts := time.Date(2026, 8, 5, 14, 3, 22, 0, time.UTC)
	err := client.Track(context.Background(), Event{
		Key:        "checkout_completed",
		UserId:     "user-1",
		Timestamp:  ts,
		Properties: map[string]any{"plan": "team"},
	})
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
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

	client := New(srv.URL, "mx_testkey", WithRetries(2))
	if err := client.Track(context.Background(), Event{Key: "app_opened"}); err != nil {
		t.Fatalf("Track after retry: %v", err)
	}
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

	client := New(srv.URL, "mx_testkey")
	err := client.TrackBatch(context.Background(), Items([]Event{{Key: "a"}, {Key: "b"}})...)
	if err != nil {
		t.Fatalf("TrackBatch: %v", err)
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
	if err := client.TrackBatch(context.Background(), nil); err != nil {
		t.Errorf("empty batch must be a no-op, got %v", err)
	}
}

func TestSetUserOmitsEmptyFields(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/user", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "mx_testkey")
	err := client.SetUser(context.Background(), User{
		Id:     "user-1",
		Name:   "Ada Lovelace",
		Custom: map[string]any{"plan": "team"},
	})
	if err != nil {
		t.Fatalf("SetUser: %v", err)
	}
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

func TestPermanentAPIErrorIsNotRetried(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusUnauthorized, `{"error":"unknown or revoked API key"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.URL, "mx_wrong", WithRetries(3))
	err := client.Track(context.Background(), Event{Key: "app_opened"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 APIError, got %v", err)
	}
	if apiErr.Message != "unknown or revoked API key" {
		t.Errorf("wrong message: %q", apiErr.Message)
	}
	if len(cap.bodies) != 1 {
		t.Errorf("401 must not be retried; got %d attempts", len(cap.bodies))
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	client := New("http://127.0.0.1:1", "mx_testkey", WithRetries(0))
	if err := client.Track(context.Background(), Event{}); !errors.Is(err, errNoEventKey) {
		t.Errorf("expected errNoEventKey, got %v", err)
	}
	if err := client.SetUser(context.Background(), User{}); !errors.Is(err, errNoUserId) {
		t.Errorf("expected errNoUserId, got %v", err)
	}
}
