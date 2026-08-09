package mixdive

import (
	"context"
	"errors"
)

// UserModelKey is the built-in model. A Model with this key updates a user
// profile rather than creating a record — use the User type, which is the
// readable way to say the same thing.
const UserModelKey = "user"

// Model is one record of a kind of thing your product tracks: a post, an
// order. Sending it creates or updates that record — Data merges key by key,
// so send only what changed.
//
// Model keys auto-register server-side on first receipt, exactly like event
// keys: there is nothing to define up front, and a model can be renamed in
// the panel afterwards without its key changing.
type Model struct {
	// Key is the model key ("post"). Unique and immutable.
	Key string
	// Id identifies this record within its model — your own id, the one
	// your database uses. Leave it empty and the server generates one,
	// which is only useful for records nothing will ever refer to again.
	Id string
	// Data is the record's fields, merged into whatever is already stored.
	// Same value rules as Event.Properties.
	Data map[string]any
	// UserId relates the record to one user in the default role "actor" —
	// for a record, usually its owner. Users adds more with a role each.
	UserId string
	Users  []RelatedUser
	// Models are other records this one references.
	Models []Ref
}

var errNoModelKey = errors.New("mixdive: model Key is required")

func (m Model) item() (itemPayload, error) {
	if m.Key == "" {
		return itemPayload{}, errNoModelKey
	}
	if m.Key == UserModelKey && m.Id == "" {
		return itemPayload{}, errNoUserId
	}
	users, refs := relatedPayloads(m.Users, m.Models)
	return itemPayload{
		Model:  m.Key,
		Id:     m.Id,
		UserId: m.UserId,
		Users:  users,
		Models: refs,
		Data:   m.Data,
	}, nil
}

// SetRecord creates or updates one record on its own, for migrations and
// nightly syncs. Track is the call to reach for when the record changed
// because something happened — it carries both facts together.
//
// A user record goes to the same endpoint as every other: /ingest/entity
// routes it to the profile write path, whereas /ingest/user expects the
// flat profile shape and would silently ignore Data.
func (c *Client) SetRecord(ctx context.Context, m Model) error {
	p, err := m.item()
	if err != nil {
		return err
	}
	return c.post(ctx, "/ingest/entity", p)
}
