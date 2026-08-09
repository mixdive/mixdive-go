package mixdive

import (
	"context"
	"errors"
)

// User is a user-profile update with merge semantics: only non-empty fields
// and the Custom keys actually present are written; nothing is cleared. Send
// only what changed.
//
// User is an Item, so a profile change can travel in the same Track call as
// the event that caused it — a follow moving two follower counts is one
// call, not three.
type User struct {
	// Id is the client-supplied user id (typically a UUID) — the same
	// value sent as Event.UserId. Required: Mixdive never invents one.
	Id string
	// Name, Username and Email update the predefined profile fields;
	// empty strings leave the stored values untouched.
	Name     string
	Username string
	Email    string
	// Custom is merged key-by-key into the stored custom data. Same
	// value rules as Event.Properties.
	Custom map[string]any
}

// profilePayload is the /ingest/user wire shape, which keeps `user_id` as
// its required field name.
type profilePayload struct {
	UserId   string         `json:"user_id"`
	Name     string         `json:"name,omitempty"`
	Username string         `json:"username,omitempty"`
	Email    string         `json:"email,omitempty"`
	Custom   map[string]any `json:"custom,omitempty"`
}

var errNoUserId = errors.New("mixdive: user Id is required")

// item renders the profile as a track item: the predefined fields join
// Custom in one Data object, which is how the server reads a "user" record.
func (u User) item() (itemPayload, error) {
	if u.Id == "" {
		return itemPayload{}, errNoUserId
	}
	data := make(map[string]any, len(u.Custom)+3)
	for k, v := range u.Custom {
		data[k] = v
	}
	for k, v := range map[string]string{"name": u.Name, "username": u.Username, "email": u.Email} {
		if v != "" {
			data[k] = v
		}
	}
	return itemPayload{Model: UserModelKey, Id: u.Id, Data: data}, nil
}

// SetUser creates or updates a user profile on its own. A nil error means
// the server queued the update (fast-ack).
func (c *Client) SetUser(ctx context.Context, u User) error {
	if u.Id == "" {
		return errNoUserId
	}
	return c.post(ctx, "/ingest/user", profilePayload{
		UserId:   u.Id,
		Name:     u.Name,
		Username: u.Username,
		Email:    u.Email,
		Custom:   u.Custom,
	})
}
