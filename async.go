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
// single background goroutine delivers calls in order using the wrapped
// Client (including its retry policy). Analytics must never slow the host
// application down — when the queue is full, the new call is dropped and
// reported to the error handler instead of applying back-pressure.
//
// One Track call is queued as one unit, so the items of a moment are never
// split apart and always reach the server together.
//
// Delivery is best-effort: calls still queued when the process exits
// without a successful Close are lost. Set Event.Id to a value derived from
// your own data (an entity id, for example) when the same action may be
// enqueued more than once — the server deduplicates on it.
//
// All methods are no-ops on a nil *Async, so an uninitialized dispatcher
// never sends anything and never panics — leave the pointer nil to keep
// analytics off.
type Async struct {
	client  *Client
	queue   chan asyncSend
	onError func(error)

	mu      sync.Mutex
	closing bool
	drained chan struct{}
}

// asyncSend is one queued call. items is a whole Track call — the facts of
// one moment, which must stay together to be related; user is a standalone
// SetUser, which has its own endpoint.
type asyncSend struct {
	items []Item
	user  *User
	what  string
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
		a.queue = make(chan asyncSend, n)
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
		queue:   make(chan asyncSend, 1024),
		onError: func(err error) { log.Printf("%v", err) },
		drained: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	go a.run()
	return a
}

// Track enqueues the facts of one moment and returns immediately — the same
// items Client.Track takes, delivered as one call so they stay related. If
// the queue is full or the dispatcher is closed, the call is dropped and
// reported to the error handler. On a nil receiver it does nothing.
func (a *Async) Track(items ...Item) {
	if a == nil || len(items) == 0 {
		return
	}
	// Copied, not aliased: delivery happens later on another goroutine, and
	// a caller reusing its slice would otherwise send whatever the buffer
	// holds by then — and race the dispatcher reading it.
	queued := append([]Item(nil), items...)
	a.enqueue(asyncSend{items: queued, what: describe(queued)})
}

// SetUser enqueues a user-profile update and returns immediately. If the
// queue is full or the dispatcher is closed, the update is dropped and
// reported to the error handler. On a nil receiver it does nothing.
func (a *Async) SetUser(u User) {
	if a == nil {
		return
	}
	a.enqueue(asyncSend{user: &u, what: fmt.Sprintf("user %q", u.Id)})
}

// describe names a queued call for the error handler: the first item, plus
// how many others rode along.
func describe(items []Item) string {
	var head string
	switch v := items[0].(type) {
	case Event:
		head = fmt.Sprintf("event %q", v.Key)
	case Model:
		head = fmt.Sprintf("record %q/%q", v.Key, v.Id)
	case User:
		head = fmt.Sprintf("user %q", v.Id)
	default:
		head = "item"
	}
	if len(items) > 1 {
		return fmt.Sprintf("%s (+%d more)", head, len(items)-1)
	}
	return head
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

func (a *Async) enqueue(send asyncSend) {
	var dropErr error
	a.mu.Lock()
	if a.closing {
		dropErr = fmt.Errorf("mixdive: async dispatcher closed, dropped %s", send.what)
	} else {
		select {
		case a.queue <- send:
		default:
			dropErr = fmt.Errorf("mixdive: async queue full, dropped %s", send.what)
		}
	}
	a.mu.Unlock()
	if dropErr != nil {
		a.onError(dropErr)
	}
}

// run is the dispatcher goroutine: it delivers queued calls in order until
// Close closes the queue, then signals drained.
func (a *Async) run() {
	defer close(a.drained)
	for send := range a.queue {
		a.deliver(send)
	}
}

// deliver sends one call, converting any panic into an error-handler report:
// an unrecovered panic on this goroutine would crash the host process, and
// analytics must never be able to do that.
func (a *Async) deliver(send asyncSend) {
	defer func() {
		if r := recover(); r != nil {
			a.onError(fmt.Errorf("mixdive: async delivery panicked: %v", r))
		}
	}()
	var err error
	switch {
	case len(send.items) > 0:
		err = a.client.Track(context.Background(), send.items...)
	case send.user != nil:
		err = a.client.SetUser(context.Background(), *send.user)
	default:
		err = errors.New("mixdive: internal: empty async send")
	}
	if err != nil {
		a.onError(fmt.Errorf("mixdive: async send of %s failed: %w", send.what, err))
	}
}
