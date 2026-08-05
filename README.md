# mixdive-go

Official Go SDK for [Mixdive](https://mixdive.com) — self-hosted product
analytics. Send an event, get its report screen automatically.

```
go get github.com/mixdive/mixdive-go
```

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

	// Set who that user is. Merge semantics: send only what changed.
	_ = client.SetUser(ctx, mixdive.User{
		Id:    "9f1c53de-6f0e-4a0b-9f2a-1c53de6f0e4a",
		Name:  "Ada Lovelace",
		Email: "ada@example.com",
	})

	// Or batch events in one request (order preserved).
	_ = client.TrackBatch(ctx, []mixdive.Event{
		{Key: "app_opened"},
		{Key: "search_run", Timestamp: time.Now()},
	})
}
```

## Semantics worth knowing

- **Fast-ack:** a nil error means the server *queued* the data (HTTP 202).
  Reports typically reflect it within a minute.
- **Safe retries:** every event carries an `occurrence_id` (auto-generated
  unless you set `Event.OccurrenceId` yourself). The server deduplicates on
  it, so the SDK's built-in retries — and your own re-sends with a fixed
  id — never double-count.
- **Retry policy:** HTTP 503 and transport errors are retried (default 2
  retries, doubling backoff from 200 ms); any other non-2xx returns an
  `*APIError` immediately.
- **User ids are yours:** Mixdive never generates user ids — pass the same
  client-supplied id (typically a UUID) to `Event.UserId` and `User.Id`.
- **Size cap:** requests over the server's limit (32 KB by default) are
  rejected — split large batches.

## Configuration

```go
client := mixdive.New(serverUrl, apiKey,
	mixdive.WithHTTPClient(&http.Client{Timeout: 3 * time.Second}),
	mixdive.WithRetries(0), // disable built-in retries
)
```

## Legacy: feedback-platform SSO helper

Earlier versions of this module shipped only an SSO-token helper for the
Mixdive feedback platform. It is unchanged and stays available:
`mixdive.NewMixDive(serverUrl, ssoSecret).CustomSSOToken(...)`.
