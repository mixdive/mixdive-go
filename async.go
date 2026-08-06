package mixdive

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
)

// Async wraps a Client so sends never block the caller: Track and SetUser
// enqueue onto a bounded in-memory queue and return immediately, while a
// single background goroutine delivers items in order using the wrapped
// Client (including its retry policy). Analytics must never slow the host
// application down — when the queue is full, the new item is dropped and
// reported to the error handler instead of applying back-pressure.
//
// Delivery is best-effort: items still queued when the process exits
// without a successful Close are lost. Set Event.OccurrenceId to a value
// derived from your own data (an entity id, for example) when the same
// action may be enqueued more than once — the server deduplicates on it.
//
// All methods are no-ops on a nil *Async, so an uninitialized dispatcher
// never sends anything and never panics — leave the pointer nil to keep
// analytics off.
type Async struct {
	client  *Client
	queue   chan asyncItem
	onError func(error)

	mu      sync.Mutex
	closing bool
	drained chan struct{}
}

// asyncItem is one queued send; exactly one of event/user is set.
type asyncItem struct {
	event *Event
	user  *User
}

// AsyncOption configures an Async dispatcher.
type AsyncOption func(*Async)

// WithQueueSize sets how many pending items the queue holds before new
// items are dropped. Default 1024; values below 1 are raised to 1.
func WithQueueSize(n int) AsyncOption {
	return func(a *Async) {
		if n < 1 {
			n = 1
		}
		a.queue = make(chan asyncItem, n)
	}
}

// WithErrorHandler replaces the default error handler (log.Printf). It
// receives queue-full drops and delivery failures after the client's
// retries are exhausted. Delivery failures are reported on the dispatcher
// goroutine, drops on the calling goroutine — keep the handler fast and
// never call the Async from inside it.
func WithErrorHandler(fn func(error)) AsyncOption {
	return func(a *Async) {
		a.onError = fn
	}
}

// NewAsync creates an Async dispatcher on top of c and starts its
// background goroutine. Call Close on shutdown to flush pending items.
func NewAsync(c *Client, opts ...AsyncOption) *Async {
	a := &Async{
		client:  c,
		queue:   make(chan asyncItem, 1024),
		onError: func(err error) { log.Printf("%v", err) },
		drained: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	go a.run()
	return a
}

// Track enqueues one event occurrence and returns immediately. If the
// queue is full or the dispatcher is closed, the event is dropped and
// reported to the error handler. On a nil receiver it does nothing.
func (a *Async) Track(e Event) {
	if a == nil {
		return
	}
	a.enqueue(asyncItem{event: &e}, fmt.Sprintf("event %q", e.Key))
}

// SetUser enqueues a user-profile update and returns immediately. If the
// queue is full or the dispatcher is closed, the update is dropped and
// reported to the error handler. On a nil receiver it does nothing.
func (a *Async) SetUser(u User) {
	if a == nil {
		return
	}
	a.enqueue(asyncItem{user: &u}, fmt.Sprintf("user %q", u.Id))
}

// Close stops accepting new items and waits for the queue to drain or ctx
// to expire, whichever comes first. It returns ctx.Err() if the deadline
// hit with items still pending. Close is idempotent, a no-op on a nil
// receiver; Track/SetUser calls after Close drop their items.
func (a *Async) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if !a.closing {
		a.closing = true
		close(a.queue)
	}
	a.mu.Unlock()

	select {
	case <-a.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Async) enqueue(item asyncItem, what string) {
	var dropErr error
	a.mu.Lock()
	if a.closing {
		dropErr = fmt.Errorf("mixdive: async dispatcher closed, dropped %s", what)
	} else {
		select {
		case a.queue <- item:
		default:
			dropErr = fmt.Errorf("mixdive: async queue full, dropped %s", what)
		}
	}
	a.mu.Unlock()
	if dropErr != nil {
		a.onError(dropErr)
	}
}

// run is the dispatcher goroutine: it delivers queued items in order until
// Close closes the queue, then signals drained.
func (a *Async) run() {
	defer close(a.drained)
	for item := range a.queue {
		var err error
		switch {
		case item.event != nil:
			if err = a.client.Track(context.Background(), *item.event); err != nil {
				err = fmt.Errorf("mixdive: async send of event %q failed: %w", item.event.Key, err)
			}
		case item.user != nil:
			if err = a.client.SetUser(context.Background(), *item.user); err != nil {
				err = fmt.Errorf("mixdive: async send of user %q failed: %w", item.user.Id, err)
			}
		default:
			err = errors.New("mixdive: internal: empty async item")
		}
		if err != nil {
			a.onError(err)
		}
	}
}
