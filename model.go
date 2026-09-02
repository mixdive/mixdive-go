package mixdive

import (
	"errors"
	"time"
)

// UserModelKey is the built-in model. A record with this key updates a user
// profile rather than creating a record — use SetUser, which is the
// readable way to say the same thing.
const UserModelKey = "user"

// Model is one record of a kind of thing your product tracks: a post, an
// order. Start it with NewModel and complete it with setters; sending it
// creates or updates that record — data merges key by key, so send only
// what changed.
type Model struct {
	key    string
	id     string
	userId string
	users  []userPayload
	models []refPayload
	data   map[string]any
	inc    map[string]float64
}

// NewModel starts a record: key is the model key ("post") — unique,
// immutable, auto-registering server-side on first receipt exactly like an
// event key — and id identifies this record within it, your own id, the one
// your database uses. Both are required.
//
//	client.Track(
//	    mixdive.NewEvent("post_created").SetId("post-created-p1").SetEventUser("u9"),
//	    mixdive.NewModel("post", "p1").SetDataString("kind", "photo"))
//
// A record is not safe for concurrent use and must not be modified after
// it is handed to Track.
func NewModel(key, id string) *Model {
	return &Model{key: key, id: id}
}

// SetUser relates the record to one user in the default role "actor" —
// for a record, usually its owner.
func (m *Model) SetUser(userId string) *Model {
	m.userId = userId
	return m
}

// AddUser attaches a further user with a role saying why. An empty role
// means the default "actor"; an empty userId is ignored.
func (m *Model) AddUser(userId, role string) *Model {
	if userId != "" {
		m.users = append(m.users, userPayload{Id: userId, Role: role})
	}
	return m
}

// SetRelation relates this record to another one it references. Call it
// once per related record; relating a record that does not exist yet
// creates it. An empty model or id is ignored.
func (m *Model) SetRelation(model, id string) *Model {
	if model != "" && id != "" {
		m.models = append(m.models, refPayload{Model: model, Id: id})
	}
	return m
}

// SetDataString sets one string field of the record, merged into whatever
// is already stored.
func (m *Model) SetDataString(key, value string) *Model {
	return m.setData(key, value)
}

// SetDataInt sets one integer field of the record.
func (m *Model) SetDataInt(key string, value int) *Model {
	return m.setData(key, value)
}

// SetDataFloat sets one float field of the record.
func (m *Model) SetDataFloat(key string, value float64) *Model {
	return m.setData(key, value)
}

// SetDataBool sets one boolean field of the record.
func (m *Model) SetDataBool(key string, value bool) *Model {
	return m.setData(key, value)
}

// SetDataTimestamp sets one point-in-time field of the record, sent as
// RFC 3339 in UTC — SetDataTimestamp("created_at", post.CreatedAt).
func (m *Model) SetDataTimestamp(key string, value time.Time) *Model {
	return m.setData(key, value.UTC().Format(time.RFC3339))
}

// SetDataAny sets one field of any JSON-encodable shape: string, number,
// boolean, or object nested at most 2 levels — arrays and deeper nesting
// are dropped server-side. The typed setters are this method with the type
// spelled out; reach for this one for objects.
func (m *Model) SetDataAny(key string, value any) *Model {
	return m.setData(key, value)
}

func (m *Model) setData(key string, value any) *Model {
	if m.data == nil {
		m.data = map[string]any{}
	}
	m.data[key] = value
	return m
}

// IncrementData adds by (which may be negative) to one numeric field of the
// record, server-side — no need to know the current value. Repeated calls
// for the same key sum up before sending.
//
// Every send carries a generated increment id the server deduplicates on,
// so the SDK's own HTTP retries never double-apply. Two separate Track
// calls are two increments, though — when your delivery pipeline may replay
// the same action (a task queue redelivering, for example), prefer setting
// the absolute value with SetDataInt, which is idempotent by construction.
//
// Incrementing a field that currently holds a non-number fails the item
// server-side.
func (m *Model) IncrementData(key string, by float64) *Model {
	if m.inc == nil {
		m.inc = map[string]float64{}
	}
	m.inc[key] += by
	return m
}

var (
	errNoModelKey = errors.New("mixdive: model key is required")
	errNoModelId  = errors.New("mixdive: model id is required")
)

func (m *Model) item() (itemPayload, error) {
	if m == nil {
		return itemPayload{}, errNilItem
	}
	if m.key == "" {
		return itemPayload{}, errNoModelKey
	}
	if m.id == "" {
		if m.key == UserModelKey {
			return itemPayload{}, errNoUserId
		}
		return itemPayload{}, errNoModelId
	}
	p := itemPayload{
		Model:   m.key,
		Id:      m.id,
		UserId:  m.userId,
		Users:   m.users,
		Models:  m.models,
		Data:    m.data,
		DataInc: m.inc,
	}
	if len(m.inc) > 0 {
		// A fresh id per conversion: each Track call is one increment, and
		// the marshaled body the client retries carries the same id.
		p.IncId = newItemId()
	}
	return p, nil
}
