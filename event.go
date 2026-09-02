package mixdive

import (
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
	deviceId   string
	sessionId  string
	users      []userPayload
	models     []refPayload
	timestamp  time.Time
	count      int64
	sum        float64
	duration   float64 // seconds
	properties map[string]any
}

// NewEvent starts an occurrence of the event key — unique, immutable,
// auto-registering server-side on first receipt ("checkout_completed").
// Everything else is optional and added with setters, each returning the
// event for chaining:
//
//	client.Track(mixdive.NewEvent("post_liked").
//	    SetId("like-u9-p1").
//	    SetEventUser("u9").
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

// SetEventUser ties the occurrence to the user who did it — one user in the
// default role "actor". Ids are always supplied by your systems (typically
// UUIDs — Mixdive never generates them); without one the occurrence is
// anonymous, counting toward event totals only. It is the same id the
// SetUser profile constructor takes; the names differ so a Track call
// reading both stays unambiguous.
func (e *Event) SetEventUser(userId string) *Event {
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

// SetDevice ties the occurrence to the anonymous device it came from —
// the client-generated persistent id behind visitor analytics (max 64
// chars; Mixdive never generates one). A backend proxying client events
// passes it through per item; there is deliberately no process-wide
// default, which would merge every user's traffic into one visitor. An
// item carrying both a device and a user identifies the device as that
// user, and the server merges the device's anonymous history into them.
func (e *Event) SetDevice(deviceId string) *Event {
	e.deviceId = deviceId
	return e
}

// SetSession ties the occurrence to the client's session id (renewed
// client-side after 30 minutes of inactivity; max 64 chars). Meaningless
// without SetDevice — the server ignores a session without a device.
func (e *Event) SetSession(sessionId string) *Event {
	e.sessionId = sessionId
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

// SetCount folds repeated happenings into this one occurrence — "bought 3
// items" is one occurrence with a count of 3, and it moves every counter
// by 3. Unset means 1; values below 2 are not sent (the server reads
// anything but a positive whole number as 1, capped at 1 000 000).
func (e *Event) SetCount(count int) *Event {
	if count > 1 {
		e.count = int64(count)
	} else {
		e.count = 0
	}
	return e
}

// SetSum accumulates a number into the event's sum total — the money of a
// purchase, the points of a score. Negative values subtract (a refund);
// the report shows the range's total and per-event average. Zero is not
// sent.
func (e *Event) SetSum(sum float64) *Event {
	e.sum = sum
	return e
}

// SetDuration reports how long the event took, accumulated into the
// event's duration total. Sent as seconds, sub-second precision kept;
// zero and negative durations are not sent.
func (e *Event) SetDuration(d time.Duration) *Event {
	if d > 0 {
		e.duration = d.Seconds()
	} else {
		e.duration = 0
	}
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
		DeviceId:   e.deviceId,
		SessionId:  e.sessionId,
		Users:      e.users,
		Models:     e.models,
		Count:      e.count,
		Sum:        e.sum,
		Duration:   e.duration,
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

// itemPayloads converts every item up front, so a bad one fails the call
// before any bytes leave the process. A nil result means there was nothing
// to send: no items at all, or only nil ones — Track(nil) is a no-op
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
