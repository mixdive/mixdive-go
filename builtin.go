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
	// PageViewEventKey is the built-in page-view event ("a page or screen
	// was viewed") — collection addendum C4.
	PageViewEventKey = "page_view"
)

// Login starts an occurrence of the built-in login event. method is how
// they signed in ("google", "email") — the event's one recommended
// parameter; empty sends none. Complete it like any event:
//
//	client.Track(mixdive.Login("google").SetEventUser(userId))
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
//	client.Track(
//	    mixdive.SignUp("email").SetId("sign-up-"+userId).SetEventUser(userId),
//	    mixdive.SetUser(userId).SetName(name))
func SignUp(method string) *Event {
	e := NewEvent(SignUpEventKey)
	if method != "" {
		e.SetPropertyString("method", method)
	}
	return e
}

// PageView starts an occurrence of the built-in page-view event. url is the
// page or screen that was viewed — its recommended parameter, typically a
// path ("/pricing"); empty sends none. title and referrer are its other
// reserved properties, added with SetPropertyString.
//
// In browsers the Mixdive web tag sends page views automatically; from a
// backend this is the server-side-rendering case. The related built-in
// session_end is browser-only — a backend has no session lifecycle to end,
// so it has no constructor here.
func PageView(url string) *Event {
	e := NewEvent(PageViewEventKey)
	if url != "" {
		e.SetPropertyString("url", url)
	}
	return e
}
