package mixdive

import (
	"context"
	"errors"
	"time"
)

// Event is one event occurrence. Start it with NewEvent and complete it
// with setters; the zero value is unusable on purpose — the wire shape is
// the SDK's concern, not the caller's.
type Event struct {
	key        string
	id         string
	userId     string
	users      []userPayload
	models     []refPayload
	timestamp  time.Time
	properties map[string]any
}

// NewEvent starts an occurrence of the event key — unique, immutable,
// auto-registering server-side on first receipt ("checkout_completed").
// Everything else is optional and added with setters, each returning the
// event for chaining:
//
//	client.Track(ctx, mixdive.NewEvent("post_liked").
//	    SetId("like-u9-p1").
//	    SetUser("u9").
//	    SetRelation("post", "p1"))
//
// An event is not safe for concurrent use and must not be modified after
// it is handed to Track.
func NewEvent(key string) *Event {
	return &Event{key: key}
}

// SetId sets this occurrence's identity and its idempotency key (max 128
// chars). Unset, the SDK generates one per call; set it from your own data
// when the same action may be sent more than once, and the server counts
// it exactly once however often it arrives.
func (e *Event) SetId(id string) *Event {
	e.id = id
	return e
}

// SetUser ties the occurrence to the user who did it — one user in the
// default role "actor". Ids are always supplied by your systems (typically
// UUIDs — Mixdive never generates them); without one the occurrence is
// anonymous, counting toward event totals only.
func (e *Event) SetUser(userId string) *Event {
	e.userId = userId
	return e
}

// AddUser attaches a further user, with the role saying why they are on
// the event: the author of the post that was liked is role "owner". An
// empty role means the default "actor" — the one who did it; roles are
// free text and auto-register. The role is what makes "likes I gave" and
// "likes I received" two different numbers on one user's page. An empty
// userId is ignored.
func (e *Event) AddUser(userId, role string) *Event {
	if userId != "" {
		e.users = append(e.users, userPayload{Id: userId, Role: role})
	}
	return e
}

// SetRelation relates the occurrence to a record it concerns: the post a
// like was given to, which is what makes "likes this post received" a
// number. Call it once per related record. Relating a record that does not
// exist yet creates it, so relations never have to wait for anything. An
// empty model or id is ignored.
func (e *Event) SetRelation(model, id string) *Event {
	if model != "" && id != "" {
		e.models = append(e.models, refPayload{Model: model, Id: id})
	}
	return e
}

// SetTimestamp sets when the event happened. Unset, the server's receive
// time counts.
func (e *Event) SetTimestamp(t time.Time) *Event {
	e.timestamp = t
	return e
}

// SetPropertyString adds one string property.
func (e *Event) SetPropertyString(key, value string) *Event {
	return e.setProperty(key, value)
}

// SetPropertyInt adds one integer property.
func (e *Event) SetPropertyInt(key string, value int) *Event {
	return e.setProperty(key, value)
}

// SetPropertyFloat adds one float property.
func (e *Event) SetPropertyFloat(key string, value float64) *Event {
	return e.setProperty(key, value)
}

// SetPropertyBool adds one boolean property.
func (e *Event) SetPropertyBool(key string, value bool) *Event {
	return e.setProperty(key, value)
}

// SetPropertyTimestamp adds one point-in-time property, sent as RFC 3339
// in UTC.
func (e *Event) SetPropertyTimestamp(key string, value time.Time) *Event {
	return e.setProperty(key, value.UTC().Format(time.RFC3339))
}

// SetPropertyAny adds one property of any JSON-encodable shape: string,
// number, boolean, or object nested at most 2 levels — arrays and deeper
// nesting are dropped server-side. The typed setters are this method with
// the type spelled out; reach for this one for objects.
func (e *Event) SetPropertyAny(key string, value any) *Event {
	return e.setProperty(key, value)
}

func (e *Event) setProperty(key string, value any) *Event {
	if e.properties == nil {
		e.properties = map[string]any{}
	}
	e.properties[key] = value
	return e
}

var errNoEventKey = errors.New("mixdive: event key is required")

func (e *Event) item() (itemPayload, error) {
	if e == nil {
		return itemPayload{}, errNilItem
	}
	if e.key == "" {
		return itemPayload{}, errNoEventKey
	}
	p := itemPayload{
		Event:      e.key,
		Id:         e.id,
		UserId:     e.userId,
		Users:      e.users,
		Models:     e.models,
		Properties: e.properties,
	}
	if p.Id == "" {
		p.Id = newItemId()
	}
	if !e.timestamp.IsZero() {
		ts := e.timestamp.UTC()
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
//	    mixdive.NewEvent("post_created").SetId("post-created-p1").SetUser("u9"),
//	    mixdive.NewModel("post", "p1").SetDataString("kind", "photo"))
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
	return c.trackPayloads(ctx, payloads)
}

// trackPayloads routes converted items. One item that is a plain event goes
// to the endpoint built for it: there is nothing for it to be related to,
// and older servers know it. Everything else travels as a track envelope.
func (c *Client) trackPayloads(ctx context.Context, payloads []itemPayload) error {
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
// to send: no items at all, or only nil ones — Track(ctx, nil) is a no-op
// rather than a panic. A typed nil pointer, though, is an error: it means
// a call site built an item and lost it.
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
