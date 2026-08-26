package mixdive

import (
	"errors"
	"time"
)

// Item is one fact you can send: an *Event, a *Model record, or a *User
// profile. Track takes any mix of them, which is how an event and the data
// it concerns travel together.
//
// Items are built with NewEvent, NewModel and NewUser and completed with
// setters — the wire shape is the SDK's concern, not the caller's. The
// interface is deliberately closed: its only method is unexported, so the
// set of item kinds is exactly the ones this package defines and stays in
// step with the ingest contract.
type Item interface {
	item() (itemPayload, error)
}

// errNilItem is returned when a typed nil pointer (a nil *Event, say)
// reaches Track — almost always a bug at the call site, unlike an untyped
// nil Item, which is skipped.
var errNilItem = errors.New("mixdive: nil item")

// itemPayload is the wire shape of one item (docs/ingest-api.md). Exactly
// one of Event/Model is set.
type itemPayload struct {
	Event string `json:"event,omitempty"`
	Model string `json:"model,omitempty"`

	Id     string        `json:"id,omitempty"`
	UserId string        `json:"user_id,omitempty"`
	Users  []userPayload `json:"users,omitempty"`
	Models []refPayload  `json:"models,omitempty"`

	Timestamp  *time.Time     `json:"timestamp,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	Data       map[string]any `json:"data,omitempty"`

	// DataInc carries server-side additions to numeric data fields, and
	// IncId is the increment's idempotency id the server deduplicates on
	// (docs/ingest-api.md). Only model records carry them.
	DataInc map[string]float64 `json:"data_inc,omitempty"`
	IncId   string             `json:"inc_id,omitempty"`

	// DeviceId/SessionId are the collection addendum's visitor fields
	// (docs/collection-addendum.md, C1/C2) — client-generated identities a
	// backend passes through per item, never defaults process-wide.
	DeviceId  string `json:"device_id,omitempty"`
	SessionId string `json:"session_id,omitempty"`
}

// userPayload is one entry of the wire contract's `users` list: a user
// related to the item, and the role saying why. An empty role means the
// default "actor".
type userPayload struct {
	Id   string `json:"id"`
	Role string `json:"role,omitempty"`
}

// refPayload is one entry of the wire contract's `models` list: a record
// the item touches but does not itself describe.
type refPayload struct {
	Model string `json:"model"`
	Id    string `json:"id"`
}

// Items adapts a homogeneous slice for the variadic Track/TrackBatch calls:
//
//	client.TrackBatch(ctx, mixdive.Items(events)...)
func Items[T Item](s []T) []Item {
	out := make([]Item, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
