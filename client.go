package mixdive

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client sends analytics data to a Mixdive server and is built so it can
// never get in the host application's way: every call enqueues onto a
// bounded in-memory queue and returns immediately, while a single background
// goroutine delivers the calls in order to the server's fast-ack ingest API,
// retrying transient failures. Nothing is ever returned to the caller —
// failures go to the error handler (default: log.Printf) — and when the
// queue is full the oldest queued call is dropped to make room, never
// applying back-pressure to the application.
//
// One Track call is queued as one unit, so the items of a moment are never
// split apart and always reach the server together.
//
// Every method is a safe no-op on a nil *Client, and New called without a
// server URL or an API key returns a client that is off (announced once,
// then silent). Either way, an unconfigured or uninitialized client never
// sends anything, never blocks and never panics — leave the pointer nil, or
// leave the configuration empty, to keep analytics off.
//
// Delivery is best-effort: calls still queued when the process exits without
// a successful Close are lost. Give an event an id derived from your own
// data (SetId with an entity id, for example) when the same action may be
// enqueued more than once — the server deduplicates on it.
type Client struct {
	tr        *transport
	onError   func(error)
	queueSize int
	off       bool

	queue chan send

	mu      sync.Mutex
	closing bool
	drained chan struct{}
}

// send is one queued call, already rendered to its wire shape. Exactly one
// of the payload fields is set: items is a whole Track call — the facts of
// one moment, which must stay together to be related; batch is a TrackBatch
// call; user is a flat profile update; record is a standalone record write.
// flushed marks a Flush call's place in line instead of carrying data.
type send struct {
	items   []itemPayload
	batch   []itemPayload
	user    *profilePayload
	record  *itemPayload
	flushed chan<- struct{}
	what    string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client (10 s timeout). Keep a
// timeout on whatever you pass in — it is what bounds a send to a hung
// server.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.tr.httpClient = h }
}

// WithRetries sets how many times a delivery is retried after a retryable
// failure — HTTP 503 or a transport error. Default 2; 0 disables retries.
// Retrying is safe: every event carries an id the server deduplicates on,
// and record merges are idempotent by construction.
func WithRetries(n int) Option {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.tr.retries = n
	}
}

// WithAppVersion sets the app version reported in the X-App-Version header
// on every ingest call, which Mixdive stamps on each occurrence. When this
// option is not used, the client defaults to the host binary's VCS revision
// (the commit it was built from, when available). Pass "" to send no
// version at all.
func WithAppVersion(v string) Option {
	return func(c *Client) { c.tr.appVersion = v }
}

// WithQueueSize sets how many pending calls the queue holds. Default 1024;
// values below 1 are raised to 1. When the queue is full, the oldest queued
// call is dropped to admit the newest — the recent data is the valuable
// data, and analytics never pushes back on the application.
func WithQueueSize(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.queueSize = n
	}
}

// WithErrorHandler replaces the default error handler (log.Printf). It
// receives invalid-item drops, queue-overflow drops and delivery failures
// after the retries are exhausted. Delivery failures are reported on the
// dispatcher goroutine, drops on the calling goroutine — keep the handler
// fast and never call the Client from inside it. A panic in the handler is
// contained and logged, never raised into the application.
func WithErrorHandler(fn func(error)) Option {
	return func(c *Client) { c.onError = fn }
}

// New creates a Client and starts its background dispatcher. serverUrl is
// the base URL of your Mixdive server ("https://analytics.example.com");
// apiKey is an app API key created under Settings → Apps. Call Close on
// shutdown to flush what is still queued.
//
// If either serverUrl or apiKey is empty, the returned client is off: it
// reports that once through the error handler and every call on it is a
// silent no-op — misconfiguration must not take the application down, and
// must not fail later and stranger.
//
// Unless WithAppVersion overrides it, the client reports the host binary's
// VCS revision as the app version — so reports can break usage down by the
// commit that produced it, with zero configuration.
func New(serverUrl, apiKey string, opts ...Option) *Client {
	c := &Client{
		tr: &transport{
			serverUrl:  strings.TrimRight(serverUrl, "/"),
			apiKey:     apiKey,
			appVersion: detectAppVersion(),
			httpClient: &http.Client{Timeout: 10 * time.Second},
			retries:    2,
		},
		onError:   func(err error) { log.Printf("%v", err) },
		queueSize: 1024,
		drained:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.tr.serverUrl == "" || c.tr.apiKey == "" {
		c.off = true
		close(c.drained)
		c.report(errors.New("mixdive: no server URL or API key configured — analytics is off, every call is a no-op"))
		return c
	}
	c.queue = make(chan send, c.queueSize)
	go c.run()
	return c
}

// Track sends the facts of one moment in a single call: an event, the record
// it concerns, the user profiles it changes — any mix, in any order.
//
//	client.Track(
//	    mixdive.NewEvent("post_created").SetId("post-created-p1").SetEventUser("u9"),
//	    mixdive.NewModel("post", "p1").SetDataString("kind", "photo"))
//
// Items sent together are related to each other server-side, so neither has
// to name the other. Use TrackBatch for unrelated items.
//
// Track returns immediately. Items are rendered to their wire shape here, at
// enqueue time: an invalid item drops the whole call and reports it to the
// error handler, and later changes to an item never affect what was queued.
// No items is a no-op.
func (c *Client) Track(items ...Item) {
	if c == nil || c.off || len(items) == 0 {
		return
	}
	payloads, err := itemPayloads(items)
	if err != nil {
		c.report(fmt.Errorf("mixdive: dropped invalid call: %w", err))
		return
	}
	if payloads == nil {
		return
	}
	c.enqueue(send{items: payloads, what: describe(payloads)})
}

// TrackBatch sends many independent items in one request, preserving order.
// Unlike Track, items in a batch are NOT related to one another — a batch is
// many moments, not one. The whole request shares the server's size cap
// (32 KB by default) — split large batches.
//
// Like every send it returns immediately; invalid items drop the whole batch
// to the error handler, and no items is a no-op.
func (c *Client) TrackBatch(items ...Item) {
	if c == nil || c.off || len(items) == 0 {
		return
	}
	payloads, err := itemPayloads(items)
	if err != nil {
		c.report(fmt.Errorf("mixdive: dropped invalid batch: %w", err))
		return
	}
	if payloads == nil {
		return
	}
	c.enqueue(send{batch: payloads, what: fmt.Sprintf("batch of %d items", len(payloads))})
}

// SetUser creates or updates a user profile on its own and returns
// immediately. An invalid update is dropped and reported to the error
// handler.
//
// An update carrying increments travels as a user record to /ingest/entity —
// the same profile write path, reached through the endpoint whose contract
// has an increment field; everything else posts the flat /ingest/user shape.
func (c *Client) SetUser(u *User) {
	if c == nil || c.off {
		return
	}
	if u != nil && len(u.inc) > 0 {
		p, err := u.item()
		if err != nil {
			c.report(fmt.Errorf("mixdive: dropped invalid user update: %w", err))
			return
		}
		c.enqueue(send{record: &p, what: fmt.Sprintf("user %q", p.Id)})
		return
	}
	p, err := u.profile()
	if err != nil {
		c.report(fmt.Errorf("mixdive: dropped invalid user update: %w", err))
		return
	}
	c.enqueue(send{user: &p, what: fmt.Sprintf("user %q", p.UserId)})
}

// SetRecord creates or updates one record on its own, for migrations and
// nightly syncs — Track is the call to reach for when the record changed
// because something happened, since it carries both facts together. It
// returns immediately; an invalid record is dropped to the error handler.
//
// A user record goes to the same endpoint as every other: /ingest/entity
// routes it to the profile write path, whereas /ingest/user expects the
// flat profile shape and would silently ignore its data.
func (c *Client) SetRecord(m *Model) {
	if c == nil || c.off {
		return
	}
	p, err := m.item()
	if err != nil {
		c.report(fmt.Errorf("mixdive: dropped invalid record: %w", err))
		return
	}
	c.enqueue(send{record: &p, what: fmt.Sprintf("record %q/%q", p.Model, p.Id)})
}

// Flush blocks until every call enqueued before it has been delivered or
// given up on, or until ctx expires — the one deliberately waiting call,
// for a batch job or test that must not exit ahead of its data. It returns
// ctx.Err() on timeout and nil otherwise, including on a nil or off client.
// Delivery failures still go to the error handler, not to Flush.
func (c *Client) Flush(ctx context.Context) error {
	if c == nil || c.off {
		return nil
	}
	done := make(chan struct{})
	for {
		c.mu.Lock()
		if c.closing {
			c.mu.Unlock()
			select {
			case <-c.drained:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		var queued bool
		select {
		case c.queue <- send{flushed: done}:
			queued = true
		default:
			// Full of real data — a flush marker must not push any of it
			// out. Wait for the dispatcher to make room instead.
		}
		c.mu.Unlock()
		if queued {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	select {
	case <-done:
		return nil
	case <-c.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting new calls and waits for the queue to drain or ctx
// to expire, whichever comes first. It returns ctx.Err() if the deadline
// hit with calls still pending. Close is idempotent and a no-op on a nil or
// off client; Track/SetUser calls after Close drop their items.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.off {
		return nil
	}
	c.mu.Lock()
	if !c.closing {
		c.closing = true
		close(c.queue)
	}
	c.mu.Unlock()

	select {
	case <-c.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// describe names a queued call for the error handler: the first item, plus
// how many others rode along.
func describe(payloads []itemPayload) string {
	var head string
	switch p := payloads[0]; {
	case p.Event != "":
		head = fmt.Sprintf("event %q", p.Event)
	case p.Model == UserModelKey:
		head = fmt.Sprintf("user %q", p.Id)
	default:
		head = fmt.Sprintf("record %q/%q", p.Model, p.Id)
	}
	if len(payloads) > 1 {
		return fmt.Sprintf("%s (+%d more)", head, len(payloads)-1)
	}
	return head
}

// enqueue admits one call, dropping the oldest queued call when the queue
// is full — bounded memory, no back-pressure, and the freshest data wins.
// Only enqueuers add to the queue and they all hold the mutex, so after one
// removal the send always succeeds; the loop only repeats past flush
// markers and dispatcher races.
func (c *Client) enqueue(s send) {
	var dropErr error
	c.mu.Lock()
	if c.closing {
		dropErr = fmt.Errorf("mixdive: client closed, dropped %s", s.what)
	} else {
		for {
			select {
			case c.queue <- s:
			default:
				select {
				case old := <-c.queue:
					if old.flushed != nil {
						// A flush marker, not data: everything ahead of it
						// has been delivered or dropped by now, so its
						// waiter may go.
						close(old.flushed)
						continue
					}
					if dropErr == nil {
						dropErr = fmt.Errorf("mixdive: queue full, dropped oldest call (%s)", old.what)
					}
					continue
				default:
					continue // the dispatcher freed a slot meanwhile
				}
			}
			break
		}
	}
	c.mu.Unlock()
	if dropErr != nil {
		c.report(dropErr)
	}
}

// run is the dispatcher goroutine: it delivers queued calls in order until
// Close closes the queue, then signals drained.
func (c *Client) run() {
	defer close(c.drained)
	for s := range c.queue {
		c.deliver(s)
	}
}

// deliver sends one call, converting any panic into an error-handler report:
// an unrecovered panic on this goroutine would crash the host process, and
// analytics must never be able to do that.
func (c *Client) deliver(s send) {
	defer func() {
		if r := recover(); r != nil {
			c.report(fmt.Errorf("mixdive: delivery panicked: %v", r))
		}
	}()
	if s.flushed != nil {
		close(s.flushed)
		return
	}
	ctx := context.Background()
	var err error
	switch {
	case len(s.items) > 0:
		err = c.tr.trackPayloads(ctx, s.items)
	case len(s.batch) > 0:
		err = c.tr.post(ctx, "/ingest/batch", struct {
			Items []itemPayload `json:"items"`
		}{s.batch})
	case s.user != nil:
		err = c.tr.post(ctx, "/ingest/user", *s.user)
	case s.record != nil:
		err = c.tr.post(ctx, "/ingest/entity", *s.record)
	default:
		err = errors.New("mixdive: internal: empty send")
	}
	if err != nil {
		c.report(fmt.Errorf("mixdive: send of %s failed: %w", s.what, err))
	}
}

// report hands err to the error handler, containing any panic the handler
// itself raises — a monitoring hook must never be able to take the host
// application down either.
func (c *Client) report(err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("mixdive: error handler panicked: %v", r)
		}
	}()
	c.onError(err)
}
