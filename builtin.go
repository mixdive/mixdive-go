package mixdive

// Mixdive's built-in events. Like the built-in "user" model, their keys are
// defined at the Mixdive level — every project has them out of the box, with
// proper names in the panel — and they follow Google Analytics 4's
// recommended events, so an instrumented product speaks a vocabulary
// analysts already know.
//
// They are ordinary events on the wire: the constructors below return the
// same *Event NewEvent does, just pre-keyed and with the event's known
// parameters spelled out.
const (
	// LoginEventKey is the built-in login event ("a user logged in").
	LoginEventKey = "login"
	// SignUpEventKey is the built-in sign-up event ("a user signed up
	// for an account").
	SignUpEventKey = "sign_up"
)

// Login starts an occurrence of the built-in login event. method is how
// they signed in ("google", "email") — the event's one recommended
// parameter; empty sends none. Complete it like any event:
//
//	client.Track(ctx, mixdive.Login("google").SetUser(userId))
//
// A user logging in twice is two occurrences — leave the id generated
// unless your delivery pipeline redelivers, in which case SetId a value
// derived from the login itself (a session id), never the bare user id.
func Login(method string) *Event {
	e := NewEvent(LoginEventKey)
	if method != "" {
		e.SetPropertyString("method", method)
	}
	return e
}

// SignUp starts an occurrence of the built-in sign-up event. method is how
// the account was created ("google", "email"); empty sends none. A user
// signs up once, so give it a deterministic id derived from the user and
// send the new profile in the same call:
//
//	client.Track(ctx,
//	    mixdive.SignUp("email").SetId("sign-up-"+userId).SetUser(userId),
//	    mixdive.SetUser(userId).SetName(name))
func SignUp(method string) *Event {
	e := NewEvent(SignUpEventKey)
	if method != "" {
		e.SetPropertyString("method", method)
	}
	return e
}
