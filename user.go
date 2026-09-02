package mixdive

import (
	"errors"
	"time"
)

// User is a user-profile update with merge semantics: only the fields
// actually set are written; nothing is cleared. Start it with SetUser,
// complete it with setters, and send only what changed.
//
// User is an Item, so a profile change can travel in the same Track call as
// the event that caused it — a follow moving two follower counts is one
// call, not three.
type User struct {
	id           string
	name         string
	username     string
	email        string
	organization string
	phone        string
	picture      string
	gender       string
	birthYear    int
	data         map[string]any
	inc          map[string]float64
}

// SetUser starts a profile update for the user with this id — the same
// client-supplied id (typically a UUID) an event's SetEventUser takes.
// Required: Mixdive never invents a user id.
//
// It is SetUser, not NewUser, because the caller cannot know whether
// Mixdive has seen this user before — and never has to: sending is
// create-or-update either way.
//
//	client.SetUser(mixdive.SetUser(userId).
//	    SetName("Ada Lovelace").
//	    SetDataString("plan", "team"))
//
// A profile update is not safe for concurrent use and must not be modified
// after it is handed to Track or Client.SetUser.
func SetUser(id string) *User {
	return &User{id: id}
}

// SetName updates the predefined name field.
func (u *User) SetName(name string) *User {
	u.name = name
	return u
}

// SetUsername updates the predefined username field.
func (u *User) SetUsername(username string) *User {
	u.username = username
	return u
}

// SetEmail updates the predefined email field.
func (u *User) SetEmail(email string) *User {
	u.email = email
	return u
}

// SetOrganization updates the predefined organization field — the company
// or team the user belongs to.
func (u *User) SetOrganization(organization string) *User {
	u.organization = organization
	return u
}

// SetPhone updates the predefined phone field.
func (u *User) SetPhone(phone string) *User {
	u.phone = phone
	return u
}

// SetPicture updates the predefined picture field — a URL to the user's
// avatar image.
func (u *User) SetPicture(url string) *User {
	u.picture = url
	return u
}

// SetGender updates the predefined gender field — free text ("F", "male",
// whatever your product records).
func (u *User) SetGender(gender string) *User {
	u.gender = gender
	return u
}

// SetBirthYear updates the predefined birth-year field. Zero and negative
// years are not sent (the server ignores them anyway).
func (u *User) SetBirthYear(year int) *User {
	if year > 0 {
		u.birthYear = year
	} else {
		u.birthYear = 0
	}
	return u
}

// SetDataString merges one string key into the profile's custom data.
func (u *User) SetDataString(key, value string) *User {
	return u.setData(key, value)
}

// SetDataInt merges one integer key into the profile's custom data.
func (u *User) SetDataInt(key string, value int) *User {
	return u.setData(key, value)
}

// SetDataFloat merges one float key into the profile's custom data.
func (u *User) SetDataFloat(key string, value float64) *User {
	return u.setData(key, value)
}

// SetDataBool merges one boolean key into the profile's custom data.
func (u *User) SetDataBool(key string, value bool) *User {
	return u.setData(key, value)
}

// SetDataTimestamp merges one point-in-time key into the profile's custom
// data, sent as RFC 3339 in UTC.
func (u *User) SetDataTimestamp(key string, value time.Time) *User {
	return u.setData(key, value.UTC().Format(time.RFC3339))
}

// SetDataAny merges one key of any JSON-encodable shape into the profile's
// custom data: string, number, boolean, or object nested at most 2 levels.
// The typed setters are this method with the type spelled out; reach for
// this one for objects.
func (u *User) SetDataAny(key string, value any) *User {
	return u.setData(key, value)
}

func (u *User) setData(key string, value any) *User {
	if u.data == nil {
		u.data = map[string]any{}
	}
	u.data[key] = value
	return u
}

// IncrementData adds by (which may be negative) to one numeric key of the
// profile's custom data, server-side — no need to know the current value.
// Repeated calls for the same key sum up before sending.
//
// Every send carries a generated increment id the server deduplicates on,
// so the SDK's own HTTP retries never double-apply. Two separate calls are
// two increments, though — when your delivery pipeline may replay the same
// action, prefer setting the absolute value with SetDataInt, which is
// idempotent by construction.
func (u *User) IncrementData(key string, by float64) *User {
	if u.inc == nil {
		u.inc = map[string]float64{}
	}
	u.inc[key] += by
	return u
}

// profilePayload is the /ingest/user wire shape, which keeps `user_id` as
// its required field name.
type profilePayload struct {
	UserId       string         `json:"user_id"`
	Name         string         `json:"name,omitempty"`
	Username     string         `json:"username,omitempty"`
	Email        string         `json:"email,omitempty"`
	Organization string         `json:"organization,omitempty"`
	Phone        string         `json:"phone,omitempty"`
	Picture      string         `json:"picture,omitempty"`
	Gender       string         `json:"gender,omitempty"`
	BirthYear    int            `json:"birth_year,omitempty"`
	Custom       map[string]any `json:"custom,omitempty"`
}

var errNoUserId = errors.New("mixdive: user id is required")

// item renders the profile as a track item: the predefined fields join
// the custom data in one object, which is how the server reads a "user"
// record.
func (u *User) item() (itemPayload, error) {
	if u == nil {
		return itemPayload{}, errNilItem
	}
	if u.id == "" {
		return itemPayload{}, errNoUserId
	}
	data := make(map[string]any, len(u.data)+8)
	for k, v := range u.data {
		data[k] = v
	}
	for k, v := range map[string]string{
		"name": u.name, "username": u.username, "email": u.email,
		"organization": u.organization, "phone": u.phone,
		"picture": u.picture, "gender": u.gender,
	} {
		if v != "" {
			data[k] = v
		}
	}
	if u.birthYear > 0 {
		data["birth_year"] = u.birthYear
	}
	p := itemPayload{Model: UserModelKey, Id: u.id, Data: data, DataInc: u.inc}
	if len(u.inc) > 0 {
		p.IncId = newItemId()
	}
	return p, nil
}

// profile renders the update in the flat /ingest/user shape. Increments
// cannot ride on it — that endpoint has no increment field, which is why
// Client.SetUser routes an increment-carrying update as a user record
// instead.
func (u *User) profile() (profilePayload, error) {
	if u == nil {
		return profilePayload{}, errNilItem
	}
	if u.id == "" {
		return profilePayload{}, errNoUserId
	}
	return profilePayload{
		UserId:       u.id,
		Name:         u.name,
		Username:     u.username,
		Email:        u.email,
		Organization: u.organization,
		Phone:        u.phone,
		Picture:      u.picture,
		Gender:       u.gender,
		BirthYear:    u.birthYear,
		Custom:       u.data,
	}, nil
}
