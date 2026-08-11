// Package mixdive is the official Go SDK for Mixdive, the self-hosted
// product analytics tool: create a Client with an app API key, then send
// what happened in your product to your Mixdive server.
//
// There are three kinds of thing to send, and Track takes any mix of them.
// Each is started by its constructor and completed with chainable setters —
// the wire shape is the SDK's concern, not the caller's:
//
//   - NewEvent(key)      — something happened ("post_created").
//   - NewModel(key, id)  — a record of a thing your product tracks
//     ("post" p1), merged field by field into whatever is already stored.
//   - SetUser(id)        — a profile, the one built-in model. Set, not New:
//     sending is create-or-update either way.
//
// Items sent in one Track call are related to one another server-side, so
// the event and the record it concerns travel together and neither has to
// name the other:
//
//	client.Track(ctx,
//	    mixdive.NewEvent("post_created").SetId("post-created-p1").SetUser("u9"),
//	    mixdive.NewModel("post", "p1").SetDataString("kind", "photo"))
//
// The ids differ on purpose: occurrence ids share one namespace across every
// event key, so an event id must be unique per occurrence, while the record
// carries your own post id. They are related by travelling together.
//
// An event can also relate to records it merely touches, and attach several
// users with a role each — that is what makes "likes this post received" and
// "likes this author received" different numbers:
//
//	client.Track(ctx, mixdive.NewEvent("post_liked").
//	    SetId("like-u9-p1").
//	    SetUser("u9").
//	    AddUser("u_author", "owner").
//	    SetRelation("post", "p1"))
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
