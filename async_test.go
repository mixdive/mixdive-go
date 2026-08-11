package mixdive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAsyncDeliversInOrderAndDrains(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":true}`))
	mux.Handle("POST /ingest/user", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := NewAsync(New(srv.URL, "mx_testkey"))
	a.Track(NewEvent("first"))
	a.SetUser(SetUser("user-1").SetUsername("ada"))
	a.Track(NewEvent("second"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.bodies) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(cap.bodies))
	}
	if cap.bodies[0]["event_key"] != "first" ||
		cap.bodies[1]["user_id"] != "user-1" ||
		cap.bodies[2]["event_key"] != "second" {
		t.Errorf("wrong order/payloads: %v", cap.bodies)
	}
}

func TestAsyncTrackDoesNotBlockOnSlowServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	defer close(release)

	a := NewAsync(New(srv.URL, "mx_testkey", WithRetries(0)))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			a.Track(NewEvent("burst"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Track blocked the caller while the server hung")
	}
}

func TestAsyncQueueFullDropsAndReports(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	defer close(release)

	var mu sync.Mutex
	var errs []error
	a := NewAsync(New(srv.URL, "mx_testkey", WithRetries(0)),
		WithQueueSize(1),
		WithErrorHandler(func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}))

	// One item may be in-flight and one queued; the rest must drop.
	for i := 0; i < 10; i++ {
		a.Track(NewEvent("burst"))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(errs) < 8 {
		t.Fatalf("expected at least 8 queue-full drops, got %d", len(errs))
	}
	for _, err := range errs {
		if !strings.Contains(err.Error(), "queue full") {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestAsyncReportsDeliveryFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unknown or revoked API key"}`))
	}))
	defer srv.Close()

	var mu sync.Mutex
	var errs []error
	a := NewAsync(New(srv.URL, "mx_wrong", WithRetries(0)),
		WithErrorHandler(func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}))
	a.Track(NewEvent("app_opened"))
	a.SetUser(SetUser("")) // invalid: missing id — reported at enqueue, not a panic

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 2 {
		t.Fatalf("expected 2 reported errors, got %d: %v", len(errs), errs)
	}
	// The 401 arrives on the dispatcher goroutine, the invalid update on the
	// calling one — both must be reported, in whichever order they landed.
	all := errs[0].Error() + " | " + errs[1].Error()
	if !strings.Contains(all, "401") || !strings.Contains(all, "user id is required") {
		t.Errorf("expected a 401 delivery failure and an invalid-user drop, got: %v", errs)
	}
}

func TestNilAsyncIsInertNoOp(t *testing.T) {
	var a *Async
	a.Track(NewEvent("app_opened")) // must not panic or send
	a.SetUser(SetUser("user-1"))
	if err := a.Close(context.Background()); err != nil {
		t.Errorf("nil Close must return nil, got %v", err)
	}
}

func TestAsyncCloseIsIdempotentAndRejectsLateItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	var mu sync.Mutex
	var errs []error
	a := NewAsync(New(srv.URL, "mx_testkey"),
		WithErrorHandler(func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := a.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	a.Track(NewEvent("late"))
	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "closed") {
		t.Errorf("late Track must report a drop, got: %v", errs)
	}
}

// A caller reusing one []Item buffer across Track calls must not have its
// earlier sends rewritten by later ones — delivery happens later, on another
// goroutine, so the call is rendered to its wire shape at enqueue time.
func TestAsyncTrackCopiesTheCallersSlice(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":1}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := NewAsync(New(srv.URL, "mx_testkey"))
	buf := make([]Item, 1)
	for _, key := range []string{"first", "second", "third"} {
		buf[0] = NewEvent(key)
		a.Track(buf...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.bodies) != 3 {
		t.Fatalf("expected 3 deliveries, got %d", len(cap.bodies))
	}
	for i, want := range []string{"first", "second", "third"} {
		if got := cap.bodies[i]["event_key"]; got != want {
			t.Errorf("delivery %d: got %v, want %q — the slice was aliased", i, got, want)
		}
	}
}
