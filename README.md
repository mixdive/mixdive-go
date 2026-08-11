# mixdive-go

Official Go SDK for [Mixdive](https://mixdive.com) — self-hosted product
analytics. Send an event, get its report screen automatically.

```
go get github.com/mixdive/mixdive-go
```

> Looking for the Mixdive **feedback platform**? That's a separate product
> with its own SDK: [`github.com/mixdive/mixdive-feedback-go`](https://github.com/mixdive/mixdive-feedback-go).
> The two are independent and can be used side by side.

## Quick start

Create an app under **Settings → Apps** in your Mixdive panel to get an API
key, then:

```go
package main

import (
	"context"
	"time"

	"github.com/mixdive/mixdive-go"
)

func main() {
	client := mixdive.New("https://analytics.example.com", "mx_your_api_key")
	ctx := context.Background()

	// Send an event. Unknown keys auto-register on first receipt —
	// no schema setup needed. Every item starts with its constructor and
	// is completed with chainable setters.
	_ = client.Track(ctx, mixdive.NewEvent("checkout_completed").
		SetUser("9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a").
		SetPropertyString("plan", "team").
		SetPropertyInt("seats", 4))

	// Send the event AND the record it concerns, together. Items in one
	// Track call are related to each other server-side, so post p1's
	// screen shows this occurrence without either item naming the other.
	_ = client.Track(ctx,
		mixdive.NewEvent("post_created").SetId("post-created-p1").SetUser("u9"),
		mixdive.NewModel("post", "p1").SetDataString("kind", "photo"),
	)

	// Set who that user is. Merge semantics: send only what changed.
	// SetUser, not NewUser — you never have to know whether Mixdive has
	// seen this user before.
	_ = client.SetUser(ctx, mixdive.SetUser("9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a").
		SetName("Ada Lovelace").
		SetEmail("ada@example.com"))

	// Or batch unrelated items in one request (order preserved, no
	// relations created between them).
	_ = client.TrackBatch(ctx, mixdive.Items([]*mixdive.Event{
		mixdive.NewEvent("app_opened"),
		mixdive.NewEvent("search_run").SetTimestamp(time.Now()),
	})...)
}
```

## Events, records and users

`Track` takes any mix of three item types, each started by its constructor
and completed with setters:

| Constructor | Means | Id |
|---|---|---|
| `NewEvent(key)` | something happened | `SetId` — the occurrence id, generated if you never set it |
| `NewModel(key, id)` | a record of a thing you track (`post` p1) | required at construction — your own id, the one your database uses |
| `SetUser(id)` | a profile — the one built-in model | required at construction; Mixdive never invents a user id |

Fields go on with typed setters — `SetPropertyString`, `SetPropertyInt`,
`SetPropertyFloat`, `SetPropertyBool`, `SetPropertyTimestamp` (takes a
`time.Time`), and `SetPropertyAny` for objects — with the same family named
`SetData…` on records and profiles.

Model keys, event keys and roles all auto-register on first receipt. There is
nothing to define up front and no schema to keep in sync.

### Built-in events

Like the built-in `user` model, some events are defined at the Mixdive
level — every project has them out of the box, named in the panel before
their first occurrence. They follow [GA4's recommended
events](https://developers.google.com/analytics/devguides/collection/ga4/reference/events),
and each has a constructor that pre-fills the key and takes the event's
recommended `method` parameter:

```go
_ = client.Track(ctx, mixdive.Login("google").SetUser(userId))
_ = client.Track(ctx,
	mixdive.SignUp("email").SetId("sign-up-"+userId).SetUser(userId),
	mixdive.SetUser(userId).SetName(name))
```

They return an ordinary `*Event`, so every event setter chains as usual.

### Several users, with roles

An item can relate to more than one user, and the role says why. `SetUser`
is shorthand for one user in the default role `actor` — the one who did it.
`AddUser` attaches the others; any role but `actor` reads as "this was done
to them".

```go
_ = client.Track(ctx, mixdive.NewEvent("post_liked").
	SetId("like-u9-p1"). // unique per like, not the post id
	SetUser("u9").
	AddUser("u_author", "owner").
	SetRelation("post", "p1"))
```

That one call answers three questions in the panel: how many likes `u9` gave
(their `actor` counter), how many `u_author` received (their `owner` counter),
and how many post `p1` received (the post's own counter).

`SetRelation` names records the event *touches* but does not describe (call
it once per record). Relating a record that does not exist yet creates it —
empty, with counters — and it fills in whenever its data arrives, in any
order.

### Profile changes that belong to an event

A user profile is an item too, so a change caused by an event travels with it:

```go
_ = client.Track(ctx,
	mixdive.NewEvent("user_followed").SetId("f-a-b").SetUser("a").AddUser("b", "target"),
	mixdive.SetUser("a").SetDataInt("following_count", 12),
	mixdive.SetUser("b").SetDataInt("follower_count", 44),
)
```

### Increments

When you don't know a field's current value, add to it instead of setting
it — the server does the arithmetic:

```go
_ = client.Track(ctx,
	mixdive.NewEvent("post_viewed").SetUser("u9").SetRelation("post", "p1"),
	mixdive.NewModel("post", "p1").IncrementData("view_count", 1),
)
```

Each send carries a generated increment id the server deduplicates on, so
the SDK's built-in HTTP retries never double-apply. Two separate `Track`
calls are two increments, though — if your pipeline can replay the same
action (a task queue redelivering), prefer the idempotent absolute set
(`SetDataInt`). Incrementing a field that currently holds a non-number
fails that item server-side.

## Semantics worth knowing

- **Fast-ack:** a nil error means the server *queued* the data (HTTP 202).
  Reports typically reflect it within a minute.
- **Safe retries:** every event carries an id (auto-generated unless you
  call `SetId` yourself). The server deduplicates on it, so the SDK's
  built-in retries — and your own re-sends with a fixed id — never
  double-count. Re-sending a record with a known id merges into it rather
  than creating a second one.
- **Track relates, TrackBatch does not:** one `Track` call is one moment and
  its items are related to each other; a batch is many independent moments.
- **Retry policy:** HTTP 503 and transport errors are retried (default 2
  retries, doubling backoff from 200 ms); any other non-2xx returns an
  `*APIError` immediately.
- **User ids are yours:** Mixdive never generates user ids — pass the same
  client-supplied id (typically a UUID) to an event's `SetUser` and to the
  `SetUser` profile constructor.
- **Size cap:** requests over the server's limit (32 KB by default) are
  rejected — split large batches.

## Non-blocking sending

`Track`/`SetUser` on `Client` are synchronous HTTP calls. When analytics
must never slow your request path down, wrap the client in an `Async`
dispatcher: calls enqueue onto a bounded in-memory queue and return
immediately, and a background goroutine delivers them in order (with the
client's usual retries). A full queue drops the newest item — analytics
never applies back-pressure to your application.

```go
client := mixdive.New("https://analytics.example.com", "mx_your_api_key")
analytics := mixdive.NewAsync(client,
	mixdive.WithQueueSize(4096),                       // default 1024
	mixdive.WithErrorHandler(func(err error) { ... }), // default log.Printf
)

// Fire-and-forget — returns immediately, never blocks. One Track call is
// queued as one unit, so a moment's items are never split apart.
analytics.Track(mixdive.NewEvent("checkout_completed").SetUser(userId))
analytics.Track(
	mixdive.NewEvent("post_created").SetId("post-created-"+postId).SetUser(userId),
	mixdive.NewModel("post", postId).SetDataString("kind", "photo"),
)
analytics.SetUser(mixdive.SetUser(userId).SetUsername("ada"))

// On shutdown, flush what's still queued (bounded by the context).
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
_ = analytics.Close(ctx)
```

Delivery is best-effort: items still queued when the process exits without
a successful `Close` are lost. If the same action can be enqueued more than
once (a task retry, for example), give the event an id derived from your
own data with `SetId` — the server deduplicates on it.

All methods are safe no-ops on a nil `*Async`, so gating analytics is just
not constructing it: leave the pointer nil (e.g. outside the environments
you want tracked) and every `Track`/`SetUser`/`Close` call does nothing.

## Configuration

```go
client := mixdive.New(serverUrl, apiKey,
	mixdive.WithHTTPClient(&http.Client{Timeout: 3 * time.Second}),
	mixdive.WithRetries(0), // disable built-in retries
)
```

## Upgrading from the struct-literal API

Items are now built with constructors and setters; the struct fields are no
longer exported, so the wire shape stays the SDK's concern. Mechanical
translation, nothing else changed:

```go
// before
client.Track(ctx, mixdive.Event{
	Key:        "post_created",
	Id:         "post-created-p1",
	UserId:     "u9",
	Models:     []mixdive.Ref{{Model: "post", Id: "p1"}},
	Properties: map[string]any{"kind": "photo"},
})

// after
client.Track(ctx, mixdive.NewEvent("post_created").
	SetId("post-created-p1").
	SetUser("u9").
	SetRelation("post", "p1").
	SetPropertyString("kind", "photo"))
```

Records become `NewModel(key, id)` — both halves required — with
`SetDataString`/`SetDataInt`/… instead of a `Data` map; profiles become
`SetUser(id)` with `SetName`/`SetUsername`/`SetEmail` and the same typed
`Set…Data` family instead of `Custom`. `Client.SetUser`/`SetRecord`/
`Async.SetUser` take the pointer the constructor returns. The deprecated
`Event.OccurrenceId` alias is gone — `SetId` is the one way to set an
occurrence id.

## Moved out: the feedback-platform SSO helper

Earlier versions of this module also shipped an SSO-token helper for the
Mixdive feedback platform (`MixDive`, `NewMixDive`, `CustomSSOToken`). It
now lives in its own module, so this SDK carries no dependencies at all:

```go
// before
mxd := mixdive.NewMixDive(portalUrl, ssoSecret)

// after — go get github.com/mixdive/mixdive-feedback-go
mxd := mixdivefeedback.New(portalUrl, ssoSecret)
```

`CustomSSOToken` and the tokens it mints are unchanged.

## License

Apache-2.0.
