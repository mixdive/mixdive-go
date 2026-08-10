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
	// no schema setup needed.
	_ = client.Track(ctx, mixdive.Event{
		Key:        "checkout_completed",
		UserId:     "9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a",
		Properties: map[string]any{"plan": "team", "seats": 4},
	})

	// Send the event AND the record it concerns, together. Items in one
	// Track call are related to each other server-side, so post p1's
	// screen shows this occurrence without either item naming the other.
	_ = client.Track(ctx,
		mixdive.Event{Key: "post_created", Id: "post-created-p1", UserId: "u9"},
		mixdive.Model{Key: "post", Id: "p1", Data: map[string]any{"kind": "photo"}},
	)

	// Set who that user is. Merge semantics: send only what changed.
	_ = client.SetUser(ctx, mixdive.User{
		Id:    "9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a",
		Name:  "Ada Lovelace",
		Email: "ada@example.com",
	})

	// Or batch unrelated items in one request (order preserved, no
	// relations created between them).
	_ = client.TrackBatch(ctx, mixdive.Items([]mixdive.Event{
		{Key: "app_opened"},
		{Key: "search_run", Timestamp: time.Now()},
	})...)
}
```

## Events, records and users

`Track` takes any mix of three item types:

| Type | Means | Id |
|---|---|---|
| `Event` | something happened | the occurrence id — generated if you leave it empty |
| `Model` | a record of a thing you track (`post` p1) | your own id; the server names it if omitted |
| `User` | a profile — the one built-in model | required; Mixdive never invents a user id |

Model keys, event keys and roles all auto-register on first receipt. There is
nothing to define up front and no schema to keep in sync.

### Several users, with roles

An item can relate to more than one user, and the role says why. `UserId` is
shorthand for one user in the default role `actor` — the one who did it.
Anything else reads as "this was done to them".

```go
_ = client.Track(ctx, mixdive.Event{
	Key:    "post_liked",
	Id:     "like-u9-p1", // unique per like, not the post id
	Users:  []mixdive.RelatedUser{{Id: "u9"}, {Id: "u_author", Role: "owner"}},
	Models: []mixdive.Ref{{Model: "post", Id: "p1"}},
})
```

That one call answers three questions in the panel: how many likes `u9` gave
(their `actor` counter), how many `u_author` received (their `owner` counter),
and how many post `p1` received (the post's own counter).

`Models` names records the event *touches* but does not describe. Referencing
a record that does not exist yet creates it — empty, with counters — and it
fills in whenever its data arrives, in any order.

### Profile changes that belong to an event

A `User` is an item too, so a change caused by an event travels with it:

```go
_ = client.Track(ctx,
	mixdive.Event{Key: "user_followed", Id: "f-a-b", UserId: "a",
		Users: []mixdive.RelatedUser{{Id: "b", Role: "target"}}},
	mixdive.User{Id: "a", Custom: map[string]any{"following_count": 12}},
	mixdive.User{Id: "b", Custom: map[string]any{"follower_count": 44}},
)
```

## Semantics worth knowing

- **Fast-ack:** a nil error means the server *queued* the data (HTTP 202).
  Reports typically reflect it within a minute.
- **Safe retries:** every event carries an `id` (auto-generated unless you
  set `Event.Id` yourself). The server deduplicates on it, so the SDK's
  built-in retries — and your own re-sends with a fixed id — never
  double-count. Re-sending a record with a known id merges into it rather
  than creating a second one.
- **Track relates, TrackBatch does not:** one `Track` call is one moment and
  its items are related to each other; a batch is many independent moments.
- **Retry policy:** HTTP 503 and transport errors are retried (default 2
  retries, doubling backoff from 200 ms); any other non-2xx returns an
  `*APIError` immediately.
- **User ids are yours:** Mixdive never generates user ids — pass the same
  client-supplied id (typically a UUID) to `Event.UserId` and `User.Id`.
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
analytics.Track(mixdive.Event{Key: "checkout_completed", UserId: userId})
analytics.Track(
	mixdive.Event{Key: "post_created", Id: postId, UserId: userId},
	mixdive.Model{Key: "post", Id: postId, Data: map[string]any{"kind": "photo"}},
)
analytics.SetUser(mixdive.User{Id: userId, Username: "ada"})

// On shutdown, flush what's still queued (bounded by the context).
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
_ = analytics.Close(ctx)
```

Delivery is best-effort: items still queued when the process exits without
a successful `Close` are lost. If the same action can be enqueued more than
once (a task retry, for example), set `Event.Id` to a value derived from
your own data — the server deduplicates on it.

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

## Upgrading from the first analytics release

`Track` and `TrackBatch` became variadic over `Item`. `client.Track(ctx, event)`
still compiles unchanged; `client.TrackBatch(ctx, []mixdive.Event{...})`
becomes `client.TrackBatch(ctx, mixdive.Items(events)...)`. `Event.OccurrenceId`
is deprecated in favour of `Event.Id` and still honoured.

## Moved out: the feedback-platform SSO helper

Earlier versions of this module also shipped an SSO-token helper for the
Mixdive feedback platform (`MixDive`, `NewMixDive`, `CustomSSOToken`). It
now lives in its own module, so the analytics SDK carries no dependencies:

```go
// before
mxd := mixdive.NewMixDive(portalUrl, ssoSecret)

// after — go get github.com/mixdive/mixdive-feedback-go
mxd := mixdivefeedback.New(portalUrl, ssoSecret)
```

`CustomSSOToken` and the tokens it mints are unchanged.

## License

Apache-2.0.
