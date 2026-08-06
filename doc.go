// Package mixdive is the official Go SDK for Mixdive, the self-hosted
// product analytics tool: create a Client with an app API key, then send
// event occurrences and user profiles to your Mixdive server.
//
// The SDK implements the Mixdive ingest API contract (docs/ingest-api.md in
// the Mixdive server repository). Ingestion is fast-ack: the server queues
// accepted data and reports typically reflect it within a minute. Every
// event carries an idempotency id, so retries never double-count.
//
// Client sends synchronously; wrap it in an Async dispatcher (NewAsync)
// when sends must never block the caller — calls then enqueue and return
// immediately while a background goroutine delivers them in order.
//
// The package also retains the legacy SSO-token helper (MixDive,
// CustomSSOToken) used by the Mixdive feedback platform; analytics
// integrations use Client.
package mixdive
