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
	// Id is this occurrence's identity and its idempotency key (max 128
	// chars). Leave it empty and the SDK generates one per call; set it
	// from your own data when the same action may be sent more than once,
	// and the server counts it exactly once however often it arrives.
	Id string
	// OccurrenceId is the former name of Id, still honoured. Id wins when
	// both are set.
	//
	// Deprecated: set Id instead.
	OccurrenceId string
	// UserId ties the occurrence to one user in the default role "actor".
	// Ids are always supplied by your systems (typically UUIDs — Mixdive
	// never generates them); empty means anonymous, counting toward event
	// totals only.
	UserId string
	// Users attaches further users with a role each — the liker and the
	// author of the post they liked. Merges with UserId.
	Users []RelatedUser
	// Models are the records this occurrence concerns. A like names the
	// post it was given to, which is what makes "likes this post received"
	// a number.
	Models []Ref
	// Timestamp is when the event happened. Zero means the server's
	// receive time.
	Timestamp time.Time
	// Properties is freeform JSON: string, number, boolean, or object
	// values with objects nested at most 2 levels; arrays and deeper
	// nesting are dropped server-side.
	Properties map[string]any
}

var errNoEventKey = errors.New("mixdive: event Key is required")

func (e Event) item() (itemPayload, error) {
	if e.Key == "" {
		return itemPayload{}, errNoEventKey
	}
	users, refs := relatedPayloads(e.Users, e.Models)
	p := itemPayload{
		Event:      e.Key,
		Id:         e.Id,
		UserId:     e.UserId,
		Users:      users,
		Models:     refs,
		Properties: e.Properties,
	}
	if p.Id == "" {
		p.Id = e.OccurrenceId
	}
	if p.Id == "" {
		p.Id = newItemId()
	}
	if !e.Timestamp.IsZero() {
		ts := e.Timestamp.UTC()
		p.Timestamp = &ts
	}
	return p, nil
}

// eventPayload is the /ingest/event wire shape, which keeps `event_key` as
// its required field name.
type eventPayload struct {
	itemPayload
	EventKey string `json:"event_key"`
}

// Track sends the facts of one moment in a single call: an event, the record
// it concerns, the user profiles it changes — any mix, in any order.
//
//	client.Track(ctx,
//	    mixdive.Event{Key: "post_created", Id: "post-created-p1", UserId: "u9"},
//	    mixdive.Model{Key: "post", Id: "p1", Data: map[string]any{"kind": "photo"}})
//
// Items sent together are related to each other server-side, so neither has
// to name the other. Use TrackBatch for unrelated items.
//
// A nil error means the server queued them (fast-ack); built-in retries
// reuse the same ids and never double-count. An item that fails validation
// fails the whole call before anything is sent. No items is a no-op.
func (c *Client) Track(ctx context.Context, items ...Item) error {
	payloads, err := itemPayloads(items)
	if err != nil || payloads == nil {
		return err
	}
	// One item that is a plain event goes to the endpoint built for it:
	// there is nothing for it to be related to, and older servers know it.
	if len(payloads) == 1 && payloads[0].Event != "" {
		return c.post(ctx, "/ingest/event", eventPayload{payloads[0], payloads[0].Event})
	}
	return c.post(ctx, "/ingest/track", struct {
		Items []itemPayload `json:"items"`
	}{payloads})
}

// TrackBatch sends many independent items in one request, preserving order.
// Unlike Track, items in a batch are NOT related to one another — a batch is
// many moments, not one.
//
// The whole request shares the server's size cap (32 KB by default) — split
// large batches. No items is a no-op.
//
//	client.TrackBatch(ctx, mixdive.Items(events)...)
func (c *Client) TrackBatch(ctx context.Context, items ...Item) error {
	payloads, err := itemPayloads(items)
	if err != nil || payloads == nil {
		return err
	}
	return c.post(ctx, "/ingest/batch", struct {
		Items []itemPayload `json:"items"`
	}{payloads})
}

// itemPayloads converts every item up front, so a bad one fails the call
// before any bytes leave the process. A nil result means there was nothing
// to send: no items at all, or only nil ones — Track(ctx, nil) and a slice
// of untyped nils are both no-ops rather than panics.
func itemPayloads(items []Item) ([]itemPayload, error) {
	out := make([]itemPayload, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		p, err := it.item()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
