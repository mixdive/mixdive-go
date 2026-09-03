# mixdive-go

Official Go SDK for [Mixdive](https://mixdive.com) — self-hosted product
analytics. Send an event, get its report screen automatically.

Documentation: **[docs.mixdive.com](https://docs.mixdive.com)** — concepts, integrations, the ingest API.

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

	// Send an event. Calls return immediately — a background goroutine
	// delivers them, so analytics never slows your code down. Unknown keys
	// auto-register on first receipt; there is no schema to set up.
	client.Track(mixdive.NewEvent("checkout_completed").
		SetEventUser("9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a").
		SetPropertyString("plan", "team").
		SetPropertyInt("seats", 4))

	// Send the event AND the record it concerns, together. Items in one
	// Track call are related to each other server-side, so post p1's
	// screen shows this occurrence without either item naming the other.
	client.Track(
		mixdive.NewEvent("post_created").SetId("post-created-p1").SetEventUser("u9"),
		mixdive.NewModel("post", "p1").SetDataString("kind", "photo"),
	)

	// Set who that user is. Merge semantics: send only what changed.
	// SetUser, not NewUser — you never have to know whether Mixdive has
	// seen this user before.
	client.SetUser(mixdive.SetUser("9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a").
		SetName("Ada Lovelace").
		SetEmail("ada@example.com"))

	// On shutdown, deliver what's still queued (bounded by the context).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}
```

## Analytics that never gets in the way

The client is designed so it cannot hurt the application hosting it,
whatever the analytics server is doing:

- **Every call returns immediately.** `Track`, `SetUser`, `SetRecord` and
  `TrackBatch` enqueue onto a bounded in-memory queue; one background
  goroutine delivers the calls in order, retrying transient failures
  (default 2 retries, doubling backoff). A slow or unreachable server costs
  your request path nothing.
- **Nothing to check at the call site.** Sends return no error. Failures —
  invalid items, queue overflow, delivery that failed for good — go to the
  error handler, which defaults to `log.Printf` and is replaceable with
  `WithErrorHandler`. A panic inside delivery *or inside your handler* is
  contained, never raised into your application.
- **A full queue drops the oldest call, never blocks the newest.** The
  queue holds 1024 calls by default (`WithQueueSize`); overflow is reported
  to the error handler. Analytics never applies back-pressure.
- **Off is safe, twice over.** Every method is a no-op on a nil `*Client` —
  gating analytics is just not constructing it. And `New` called with an
  empty server URL or API key returns a client that is *off*: it says so
  once through the error handler, then every call is a silent, instant
  no-op. Misconfiguration cannot crash you, block you, or fail later and
  stranger.
- **Shutdown is explicit.** `Close(ctx)` stops intake and drains the queue
  (bounded by your context); `Flush(ctx)` waits for delivery mid-run — for
  a batch job that must not exit ahead of its data. Items still queued when
  the process dies without a `Close` are lost; that trade (in-memory,
  zero dependencies, nothing written to disk) is deliberate.

One `Track` call is queued as one unit, so the items of a moment are never
split apart. If the same action can be enqueued more than once (a task
retry, for example), give the event an id derived from your own data with
`SetId` — the server deduplicates on it, so re-sends never double-count.

## Events, records and users

`Track` takes any mix of three item types, each started by its constructor
and completed with setters:

| Constructor | Means | Id |
|---|---|---|
| `NewEvent(key)` | something happened | `SetId` — the occurrence id, generated if you never set it |
| `NewModel(key, id)` | a record of a thing you track (`post` p1) | required at construction — your own id, the one your database uses |
| `SetUser(id)` | a profile — the one built-in model | required at construction; Mixdive never invents a user id |

An event names who did it with `SetEventUser(userId)` — the same
client-supplied id the `SetUser` profile constructor takes. The names
differ so a call that uses both reads unambiguously:

```go
client.Track(
	mixdive.SignUp("email").SetId("sign-up-"+userId).SetEventUser(userId),
	mixdive.SetUser(userId).SetName(name),
)
```

Fields go on with typed setters — `SetPropertyString`, `SetPropertyInt`,
`SetPropertyFloat`, `SetPropertyBool`, `SetPropertyTimestamp` (takes a
`time.Time`), and `SetPropertyAny` for objects — with the same family named
`SetData…` on records and profiles. Profiles additionally have a setter per
predefined field: `SetName`, `SetUsername`, `SetEmail`, `SetOrganization`,
`SetPhone`, `SetPicture` (an avatar URL), `SetGender` and `SetBirthYear`.

### Event measures

Three measures ride the occurrence itself — count, sum and
duration:

```go
client.Track(mixdive.NewEvent("item_purchase").
	SetEventUser(userId).
	SetCount(3).                 // one occurrence, three happenings — every counter moves by 3
	SetSum(129.90).              // accumulated into the event's sum total (money, points)
	SetDuration(90*time.Second)) // accumulated into the event's duration total
```

The report shows the range's sum and duration totals with per-event
averages; events that never send measures see no change. `SetSum` takes
negative values too (a refund subtracts).

Model keys, event keys and roles all auto-register on first receipt. There is
nothing to define up front and no schema to keep in sync.

### Built-in events

Like the built-in `user` model, some events are defined at the Mixdive
level — every project has them out of the box, named in the panel before
their first occurrence. They follow [GA4's recommended
events](https://developers.google.com/analytics/devguides/collection/ga4/reference/events),
and each has a constructor that pre-fills the key and takes the event's
recommended parameter:

```go
client.Track(mixdive.Login("google").SetEventUser(userId))
client.Track(mixdive.PageView("/pricing"))
client.Track(
	mixdive.SignUp("email").SetId("sign-up-"+userId).SetEventUser(userId),
	mixdive.SetUser(userId).SetName(name))
```

They return an ordinary `*Event`, so every event setter chains as usual.

### Several users, with roles

An item can relate to more than one user, and the role says why.
`SetEventUser` is shorthand for one user in the default role `actor` — the
one who did it. `AddUser` attaches the others; any role but `actor` reads
as "this was done to them".

```go
client.Track(mixdive.NewEvent("post_liked").
	SetId("like-u9-p1"). // unique per like, not the post id
	SetEventUser("u9").
	AddUser("u_author", "owner").
	SetDataModelRelation("post", "p1"))
```

That one call answers three questions in the panel: how many likes `u9` gave
(their `actor` counter), how many `u_author` received (their `owner` counter),
and how many post `p1` received (the post's own counter).

`SetDataModelRelation` names records the event *touches* but does not
describe (call it once per record). Relating a record that does not exist
yet creates it — empty, with counters — and it fills in whenever its data
arrives, in any order.

### Profile changes that belong to an event

A user profile is an item too, so a change caused by an event travels with it:

```go
client.Track(
	mixdive.NewEvent("user_followed").SetId("f-a-b").SetEventUser("a").AddUser("b", "target"),
	mixdive.SetUser("a").SetDataInt("following_count", 12),
	mixdive.SetUser("b").SetDataInt("follower_count", 44),
)
```

### Increments

When you don't know a field's current value, add to it instead of setting
it — the server does the arithmetic:

```go
client.Track(
	mixdive.NewEvent("post_viewed").SetEventUser("u9").SetDataModelRelation("post", "p1"),
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

- **Fast-ack:** the server queues accepted data (HTTP 202) and reports
  typically reflect it within a minute.
- **Safe retries:** every event carries an id (auto-generated unless you
  call `SetId` yourself). The server deduplicates on it, so the SDK's
  built-in retries — and your own re-sends with a fixed id — never
  double-count. Re-sending a record with a known id merges into it rather
  than creating a second one.
- **Track relates, TrackBatch does not:** one `Track` call is one moment and
  its items are related to each other; a batch is many independent moments
  sharing one request (order preserved).
- **Retry policy:** HTTP 503 and transport errors are retried (default 2
  retries, doubling backoff from 200 ms); any other non-2xx is permanent and
  goes to the error handler as an `*APIError`.
- **User ids are yours:** Mixdive never generates user ids — pass the same
  client-supplied id (typically a UUID) to an event's `SetEventUser` and to
  the `SetUser` profile constructor.
- **Size cap:** requests over the server's limit (32 KB by default) are
  rejected — split large batches.

## Configuration

```go
client := mixdive.New(serverUrl, apiKey,
	mixdive.WithHTTPClient(&http.Client{Timeout: 3 * time.Second}), // default 10 s
	mixdive.WithRetries(0),                                        // default 2
	mixdive.WithQueueSize(4096),                                   // default 1024
	mixdive.WithErrorHandler(func(err error) { /* metrics, logs */ }),
	mixdive.WithAppVersion("1.4.2"), // default: the binary's VCS revision
)
```

`WithAppVersion` feeds the `X-App-Version` header Mixdive stamps on every
occurrence and breaks reports down by; unset, it defaults to the host
binary's VCS revision (when built inside a git checkout), and `""` turns it
off.

## Upgrading from the Client/Async split

Earlier versions had a synchronous `Client` (methods took a `context.Context`
and returned an `error`) and an `Async` wrapper around it. There is now one
`Client`, and it works the way `Async` did — the industry-standard shape for
analytics SDKs: enqueue, background delivery, errors to a handler, never the
caller. Mechanical translation:

```go
// before
client := mixdive.New(serverUrl, apiKey)
analytics := mixdive.NewAsync(client, mixdive.WithQueueSize(4096))
analytics.Track(mixdive.NewEvent("app_opened").SetUser(userId))
_ = client.Track(ctx, mixdive.NewEvent("app_opened").SetUser(userId)) // sync path

// after — one client, one behavior
client := mixdive.New(serverUrl, apiKey, mixdive.WithQueueSize(4096))
client.Track(mixdive.NewEvent("app_opened").SetEventUser(userId))
```

- `NewAsync` is gone; `New` starts the dispatcher itself. `AsyncOption` and
  `Option` are one type now, so `WithQueueSize`/`WithErrorHandler` pass to
  `New` alongside `WithRetries`/`WithHTTPClient`/`WithAppVersion`.
- `*mixdive.Async` in signatures becomes `*mixdive.Client` (still nil-safe).
- The event builder's `SetUser` is now `SetEventUser` — it was too easy to
  read as the `SetUser` profile constructor sitting beside it in the same
  call. The profile constructor, `Client.SetUser` and the builders'
  `AddUser` are unchanged.
- The event builder's `SetRelation` is now `SetDataModelRelation`, naming
  what it points at. A record's `SetRelation` (record → record) keeps its
  name.
- The synchronous, error-returning calls are gone. Where you awaited a send,
  call the fire-and-forget method and use `Flush(ctx)`/`Close(ctx)` when you
  need delivery to have happened (a short-lived job, a test).
- If either constructor argument may be empty in some environments, you no
  longer need to guard construction — such a client turns itself off.

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
