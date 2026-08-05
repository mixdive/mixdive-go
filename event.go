package mixdive

import (
	"context"
	"errors"
	"time"
)

// Event is one event occurrence. Key is required; everything else is
// optional. Unknown keys auto-register server-side on first receipt — there
// are no schemas to define up front.
type Event struct {
	// Key is the unique, immutable event key ("checkout_completed").
	Key string
	// UserId ties the occurrence to a user. Ids are always supplied by
	// your systems (typically UUIDs — Mixdive never generates them);
	// empty means anonymous, counting toward event totals only.
	UserId string
	// OccurrenceId is this occurrence's idempotency id (max 128 chars).
	// Leave empty and the SDK generates one per Track/TrackBatch call —
	// set it yourself only when your own code re-sends events and needs
	// dedup across those re-sends.
	OccurrenceId string
	// Timestamp is when the event happened. Zero means the server's
	// receive time.
	Timestamp time.Time
	// Properties is freeform JSON: string, number, boolean, or object
	// values with objects nested at most 2 levels; arrays and deeper
	// nesting are dropped server-side.
	Properties map[string]any
}

// eventPayload is the wire shape (docs/ingest-api.md).
type eventPayload struct {
	EventKey     string         `json:"event_key"`
	UserId       string         `json:"user_id,omitempty"`
	OccurrenceId string         `json:"occurrence_id,omitempty"`
	Timestamp    *time.Time     `json:"timestamp,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
}

var errNoEventKey = errors.New("mixdive: event Key is required")

func (e Event) payload() (eventPayload, error) {
	if e.Key == "" {
		return eventPayload{}, errNoEventKey
	}
	p := eventPayload{
		EventKey:     e.Key,
		UserId:       e.UserId,
		OccurrenceId: e.OccurrenceId,
		Properties:   e.Properties,
	}
	if p.OccurrenceId == "" {
		p.OccurrenceId = newOccurrenceId()
	}
	if !e.Timestamp.IsZero() {
		ts := e.Timestamp.UTC()
		p.Timestamp = &ts
	}
	return p, nil
}

// Track sends one event occurrence. A nil error means the server queued it
// (fast-ack); built-in retries reuse the same occurrence id and never
// double-count.
func (c *Client) Track(ctx context.Context, e Event) error {
	p, err := e.payload()
	if err != nil {
		return err
	}
	return c.post(ctx, "/ingest/event", p)
}

// TrackBatch sends events in one request, preserving order. The whole
// request shares the server's size cap (32 KB by default) — split large
// batches. An empty slice is a no-op.
func (c *Client) TrackBatch(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	payloads := make([]eventPayload, len(events))
	for i, e := range events {
		p, err := e.payload()
		if err != nil {
			return err
		}
		payloads[i] = p
	}
	envelope := struct {
		Events []eventPayload `json:"events"`
	}{payloads}
	return c.post(ctx, "/ingest/batch", envelope)
}
