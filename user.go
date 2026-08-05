package mixdive

import (
	"context"
	"errors"
)

// User is a user-profile update with merge semantics: only non-empty
// fields and the Custom keys actually present are written; nothing is
// cleared. Send only what changed.
type User struct {
	// Id is the client-supplied user id (typically a UUID) — the same
	// value sent as Event.UserId.
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

// userPayload is the wire shape (docs/ingest-api.md).
type userPayload struct {
	UserId   string         `json:"user_id"`
	Name     string         `json:"name,omitempty"`
	Username string         `json:"username,omitempty"`
	Email    string         `json:"email,omitempty"`
	Custom   map[string]any `json:"custom,omitempty"`
}

var errNoUserId = errors.New("mixdive: user Id is required")

// SetUser creates or updates a user profile. A nil error means the server
// queued the update (fast-ack).
func (c *Client) SetUser(ctx context.Context, u User) error {
	if u.Id == "" {
		return errNoUserId
	}
	return c.post(ctx, "/ingest/user", userPayload{
		UserId:   u.Id,
		Name:     u.Name,
		Username: u.Username,
		Email:    u.Email,
		Custom:   u.Custom,
	})
}
