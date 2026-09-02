package mixdive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeliversInOrderAndDrains(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":true}`))
	mux.Handle("POST /ingest/user", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "mx_testkey")
	c.Track(NewEvent("first"))
	c.SetUser(SetUser("user-1").SetUsername("ada"))
	c.Track(NewEvent("second"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
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

func TestTrackDoesNotBlockOnSlowServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	defer close(release)

	c := New(srv.URL, "mx_testkey", WithRetries(0), WithErrorHandler(func(error) {}))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			c.Track(NewEvent("burst"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Track blocked the caller while the server hung")
	}
}

// A full queue admits the new call and drops the oldest queued one — the
// recent data is the valuable data, and the application is never slowed.
func TestQueueOverflowDropsOldestAndReports(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	defer close(release)

	ec := &errCollector{}
	c := New(srv.URL, "mx_testkey", WithRetries(0),
		WithQueueSize(1),
		WithErrorHandler(ec.add))

	// One call may be in flight and one queued; the other 8 must push their
	// predecessor out.
	for i := 0; i < 10; i++ {
		c.Track(NewEvent("burst"))
	}

	errs := ec.all()
	if len(errs) < 8 {
		t.Fatalf("expected at least 8 overflow drops, got %d", len(errs))
	}
	for _, err := range errs {
		if !strings.Contains(err.Error(), "dropped oldest") {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestReportsDeliveryFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unknown or revoked API key"}`))
	}))
	defer srv.Close()

	ec := &errCollector{}
	c := New(srv.URL, "mx_wrong", WithRetries(0), WithErrorHandler(ec.add))
	c.Track(NewEvent("app_opened"))
	c.SetUser(SetUser("")) // invalid: missing id — reported at the call, not a panic

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	errs := ec.all()
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

func TestNilClientIsInertNoOp(t *testing.T) {
	var c *Client
	c.Track(NewEvent("app_opened")) // must not panic or send
	c.TrackBatch(NewEvent("app_opened"))
	c.SetUser(SetUser("user-1"))
	c.SetRecord(NewModel("post", "p1"))
	if err := c.Flush(context.Background()); err != nil {
		t.Errorf("nil Flush must return nil, got %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Errorf("nil Close must return nil, got %v", err)
	}
}

// New without a server URL or API key yields a client that is off: it says
// so once through the error handler and every call after that is a silent,
// instant no-op — misconfigured analytics must not hurt the application.
func TestUnconfiguredClientIsOff(t *testing.T) {
	ec := &errCollector{}
	c := New("", "", WithErrorHandler(ec.add))
	c.Track(NewEvent("app_opened"))
	c.Track(NewEvent("")) // not even validation reports: the client is off
	c.SetUser(SetUser("user-1"))
	c.SetRecord(NewModel("post", "p1"))
	c.TrackBatch(NewEvent("app_opened"))
	if err := c.Flush(context.Background()); err != nil {
		t.Errorf("off Flush must return nil, got %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Errorf("off Close must return nil, got %v", err)
	}
	errs := ec.all()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "no server URL or API key") {
		t.Errorf("expected exactly the one construction notice, got: %v", errs)
	}
}

// A panicking error handler must never take the host application down — the
// panic is contained on both reporting paths (the caller's goroutine for
// drops, the dispatcher's for delivery failures).
func TestErrorHandlerPanicIsContained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "mx_wrong", WithRetries(0),
		WithErrorHandler(func(err error) { panic("handler bug") }))
	c.Track(NewEvent(""))           // drop report on the calling goroutine
	c.Track(NewEvent("app_opened")) // delivery failure on the dispatcher goroutine

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close after handler panics: %v", err)
	}
}

func TestCloseIsIdempotentAndRejectsLateItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	ec := &errCollector{}
	c := New(srv.URL, "mx_testkey", WithErrorHandler(ec.add))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	c.Track(NewEvent("late"))
	if err := c.Flush(ctx); err != nil {
		t.Errorf("Flush after Close must return nil, got %v", err)
	}
	errs := ec.all()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "closed") {
		t.Errorf("late Track must report a drop, got: %v", errs)
	}
}

// A caller reusing one []Item buffer across Track calls must not have its
// earlier sends rewritten by later ones — delivery happens later, on another
// goroutine, so the call is rendered to its wire shape at enqueue time.
func TestTrackCopiesTheCallersSlice(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":1}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "mx_testkey")
	buf := make([]Item, 1)
	for _, key := range []string{"first", "second", "third"} {
		buf[0] = NewEvent(key)
		c.Track(buf...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
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

// Flush returns only once everything enqueued before it has been attempted,
// and leaves the client fully usable — unlike Close.
func TestFlushWaitsForDeliveryAndKeepsClientOpen(t *testing.T) {
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":1}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "mx_testkey")
	for i := 0; i < 5; i++ {
		c.Track(NewEvent("warm"))
	}
	flushed(t, c)
	cap.mu.Lock()
	n := len(cap.bodies)
	cap.mu.Unlock()
	if n != 5 {
		t.Fatalf("Flush returned with %d of 5 calls delivered", n)
	}

	c.Track(NewEvent("after")) // still open after a Flush
	flushed(t, c)
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.bodies) != 6 || cap.bodies[5]["event_key"] != "after" {
		t.Errorf("the client must keep working after Flush: %v", cap.bodies)
	}
}
