// Package mixdive is the official Go SDK for Mixdive, the self-hosted
// product analytics tool: create a Client with an app API key, then send
// what happened in your product to your Mixdive server.
//
// There are three kinds of thing to send, and Track takes any mix of them:
//
//   - Event — something happened ("post_created").
//   - Model — a record of a thing your product tracks ("post" p1), merged
//     field by field into whatever is already stored.
//   - User  — a profile, the one built-in model.
//
// Items sent in one Track call are related to one another server-side, so
// the event and the record it concerns travel together and neither has to
// name the other:
//
//	client.Track(ctx,
//	    mixdive.Event{Key: "post_created", Id: "post-created-p1", UserId: "u9"},
//	    mixdive.Model{Key: "post", Id: "p1", Data: map[string]any{"kind": "photo"}})
//
// The ids differ on purpose: occurrence ids share one namespace across every
// event key, so an event id must be unique per occurrence, while the record
// carries your own post id. They are related by travelling together.
//
// An event can also name records it merely touches, and attach several users
// with a role each — that is what makes "likes this post received" and
// "likes this author received" different numbers:
//
//	client.Track(ctx, mixdive.Event{
//	    Key:    "post_liked",
//	    Id:     "like-u9-p1",
//	    Users:  []mixdive.RelatedUser{{Id: "u9"}, {Id: "u_author", Role: "owner"}},
//	    Models: []mixdive.Ref{{Model: "post", Id: "p1"}}})
//
// Nothing is defined up front: event keys, model keys and roles all
// auto-register on first receipt, and every id is optional except a user's.
//
// The SDK implements the Mixdive ingest API contract (docs/ingest-api.md in
// the Mixdive server repository). Ingestion is fast-ack: the server queues
// accepted data and reports typically reflect it within a minute. Every
// event carries an id, so retries never double-count, and record merges are
// idempotent by construction.
//
// Every call reports the client's app version in the X-App-Version header,
// so Mixdive can break reports down by release. By default that is the host
// binary's VCS revision (the commit it was built from, when the binary was
// built inside a git checkout); WithAppVersion overrides it, and
// WithAppVersion("") turns it off.
//
// Client sends synchronously; wrap it in an Async dispatcher (NewAsync)
// when sends must never block the caller — calls then enqueue and return
// immediately while a background goroutine delivers them in order.
//
// This is the SDK for Mixdive itself. Mixdive's feedback platform is a
// separate product with its own SDK, package mixdivefeedback in
// github.com/mixdive/mixdive-feedback-go — the two are independent and can
// be used side by side.
package mixdive
