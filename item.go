package mixdive

import "time"

// Item is one fact you can send: an Event, a Model record, or a User
// profile. Track takes any mix of them, which is how an event and the data
// it concerns travel together.
//
// The interface is deliberately closed — its only method is unexported, so
// the set of item kinds is exactly the ones this package defines and stays
// in step with the ingest contract.
type Item interface {
	item() (itemPayload, error)
}

// RelatedUser attaches a user to an item and says why (the wire contract's
// `users` list). Role is free text and defaults to "actor" — the one who did
// the thing. Anything else reads as "this was done to them": a post's author
// on a like is Role "owner".
//
// The role is what makes "likes I gave" and "likes I received" two different
// numbers on one user's page.
type RelatedUser struct {
	Id   string
	Role string
}

// Ref points at a record an item touches but does not itself describe: the
// post a comment was left on. Referencing a record that does not exist yet
// creates it, so references never have to wait for anything.
type Ref struct {
	Model string
	Id    string
}

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
}

type userPayload struct {
	Id   string `json:"id"`
	Role string `json:"role,omitempty"`
}

type refPayload struct {
	Model string `json:"model"`
	Id    string `json:"id"`
}

// relatedPayloads converts the shared relation fields, dropping entries with
// no id so a zero-value struct in a slice never reaches the wire.
func relatedPayloads(users []RelatedUser, refs []Ref) ([]userPayload, []refPayload) {
	var us []userPayload
	for _, u := range users {
		if u.Id == "" {
			continue
		}
		us = append(us, userPayload{Id: u.Id, Role: u.Role})
	}
	var rs []refPayload
	for _, r := range refs {
		if r.Model == "" || r.Id == "" {
			continue
		}
		rs = append(rs, refPayload{Model: r.Model, Id: r.Id})
	}
	return us, rs
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
