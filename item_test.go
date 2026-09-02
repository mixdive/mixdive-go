package mixdive

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// trackServer serves every ingest path and returns the capture behind it.
// Unexpected delivery failures fail the test through the error handler.
func trackServer(t *testing.T) (*Client, *capture) {
	t.Helper()
	cap := &capture{}
	mux := http.NewServeMux()
	mux.Handle("POST /ingest/track", cap.handler(http.StatusAccepted, `{"queued":2}`))
	mux.Handle("POST /ingest/event", cap.handler(http.StatusAccepted, `{"queued":1}`))
	mux.Handle("POST /ingest/entity", cap.handler(http.StatusAccepted, `{"queued":true}`))
	mux.Handle("POST /ingest/user", cap.handler(http.StatusAccepted, `{"queued":true}`))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL, "mx_testkey",
		WithErrorHandler(func(err error) { t.Errorf("unexpected error: %v", err) })), cap
}

func TestTrackSendsMixedItemsInOneCall(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(
		NewEvent("post_created").SetId("p1").SetEventUser("u_author"),
		NewModel("post", "p1").SetDataString("kind", "photo"),
	)
	flushed(t, client)
	if len(cap.bodies) != 1 {
		t.Fatalf("mixed items must travel in ONE call, got %d", len(cap.bodies))
	}
	items, ok := cap.bodies[0]["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 items, got %v", cap.bodies[0])
	}
	ev := items[0].(map[string]any)
	if ev["event"] != "post_created" || ev["id"] != "p1" || ev["user_id"] != "u_author" {
		t.Errorf("wrong event item: %v", ev)
	}
	rec := items[1].(map[string]any)
	if rec["model"] != "post" || rec["id"] != "p1" {
		t.Errorf("wrong model item: %v", rec)
	}
	if data, _ := rec["data"].(map[string]any); data["kind"] != "photo" {
		t.Errorf("record data lost: %v", rec["data"])
	}
	if _, present := rec["event"]; present {
		t.Error("a model record must not carry an event key")
	}
}

func TestTrackSingleEventUsesEventEndpoint(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(NewEvent("app_opened"))
	flushed(t, client)
	// The bare event endpoint requires event_key; the alias rides along.
	if got := cap.bodies[0]; got["event_key"] != "app_opened" {
		t.Errorf("single event must post the contract's event_key: %v", got)
	}
}

func TestEventCarriesDeviceAndSession(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(NewEvent("app_opened").SetDevice("d-9f2a").SetSession("s-0d9c").SetEventUser("u1"))
	flushed(t, client)
	got := cap.bodies[0]
	if got["device_id"] != "d-9f2a" || got["session_id"] != "s-0d9c" {
		t.Errorf("device/session must ride the item: %v", got)
	}
	// Unset, the fields must be absent — not empty strings.
	client.Track(NewEvent("app_opened"))
	flushed(t, client)
	plain := cap.bodies[1]
	if _, present := plain["device_id"]; present {
		t.Errorf("an unset device_id must be omitted: %v", plain)
	}
	if _, present := plain["session_id"]; present {
		t.Errorf("an unset session_id must be omitted: %v", plain)
	}
}

func TestTrackCarriesUsersWithRolesAndRefs(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(
		NewEvent("post_liked").
			SetId("like-u9-p1").
			AddUser("u9", "").
			AddUser("u_author", "owner").
			SetDataModelRelation("post", "p1"),
		NewModel("post", "p1").SetDataInt("like_count", 313),
	)
	flushed(t, client)
	ev := cap.bodies[0]["items"].([]any)[0].(map[string]any)
	users, ok := ev["users"].([]any)
	if !ok || len(users) != 2 {
		t.Fatalf("expected 2 related users, got %v", ev["users"])
	}
	if u := users[0].(map[string]any); u["id"] != "u9" {
		t.Errorf("first user wrong: %v", u)
	} else if _, hasRole := u["role"]; hasRole {
		t.Error("the default role must be omitted, not spelled out")
	}
	if u := users[1].(map[string]any); u["id"] != "u_author" || u["role"] != "owner" {
		t.Errorf("second user wrong: %v", u)
	}
	refs, ok := ev["models"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("expected 1 model reference, got %v", ev["models"])
	}
	if r := refs[0].(map[string]any); r["model"] != "post" || r["id"] != "p1" {
		t.Errorf("wrong reference: %v", r)
	}
}

func TestEventMeasuresTravelOnTheItem(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(
		NewEvent("item_purchase").SetEventUser("u1").
			SetCount(3).SetSum(129.9).SetDuration(90 * time.Second))
	flushed(t, client)
	got := cap.bodies[0]
	if got["count"] != float64(3) || got["sum"] != float64(129.9) || got["duration"] != float64(90) {
		t.Errorf("measures must ride the item: %v", got)
	}
	// The defaults are not sent: a count of 1 is the server's default, a
	// zero sum means nothing, and a negative duration is invalid.
	client.Track(NewEvent("item_purchase").SetCount(1).SetSum(0).SetDuration(-time.Second))
	flushed(t, client)
	plain := cap.bodies[1]
	for _, field := range []string{"count", "sum", "duration"} {
		if _, present := plain[field]; present {
			t.Errorf("default %s must be omitted: %v", field, plain)
		}
	}
	// Sub-second precision survives; a negative sum (a refund) is sent.
	client.Track(NewEvent("refund").SetSum(-49.5).SetDuration(1500 * time.Millisecond))
	flushed(t, client)
	refund := cap.bodies[2]
	if refund["sum"] != float64(-49.5) || refund["duration"] != float64(1.5) {
		t.Errorf("wrong refund measures: %v", refund)
	}
}

func TestProfilePredefinedFieldsTravelBothPaths(t *testing.T) {
	client, cap := trackServer(t)
	ada := func() *User {
		return SetUser("u1").SetName("Ada Lovelace").
			SetOrganization("Analytical Engines Ltd").
			SetPhone("+44 20 7946 0958").
			SetPicture("https://example.com/ada.jpg").
			SetGender("F").
			SetBirthYear(1815).
			SetDataString("plan", "team")
	}
	// The flat /ingest/user shape carries each field under its own name.
	client.SetUser(ada())
	flushed(t, client)
	flat := cap.bodies[0]
	if cap.paths[0] != "/ingest/user" {
		t.Fatalf("plain profile must post to /ingest/user, got %s", cap.paths[0])
	}
	for field, want := range map[string]any{
		"organization": "Analytical Engines Ltd", "phone": "+44 20 7946 0958",
		"picture": "https://example.com/ada.jpg", "gender": "F", "birth_year": float64(1815),
	} {
		if flat[field] != want {
			t.Errorf("flat %s = %v, want %v", field, flat[field], want)
		}
	}
	if custom, _ := flat["custom"].(map[string]any); custom["plan"] != "team" {
		t.Errorf("custom data lost: %v", flat["custom"])
	}
	// Inside Track the same fields ride the user record's data object.
	client.Track(NewEvent("sign_up"), ada())
	flushed(t, client)
	rec := cap.bodies[1]["items"].([]any)[1].(map[string]any)
	data, _ := rec["data"].(map[string]any)
	for field, want := range map[string]any{
		"organization": "Analytical Engines Ltd", "phone": "+44 20 7946 0958",
		"picture": "https://example.com/ada.jpg", "gender": "F",
		"birth_year": float64(1815), "plan": "team",
	} {
		if data[field] != want {
			t.Errorf("record data %s = %v, want %v", field, data[field], want)
		}
	}
	// An unset birth year must be absent, not zero.
	client.SetUser(SetUser("u2").SetName("Bea"))
	flushed(t, client)
	if _, present := cap.bodies[2]["birth_year"]; present {
		t.Errorf("unset birth_year must be omitted: %v", cap.bodies[2])
	}
}

func TestUserItemTravelsInsideTrack(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(
		NewEvent("user_followed").SetId("f-a-b").SetEventUser("a"),
		SetUser("a").SetDataInt("following_count", 12),
		SetUser("b").SetName("Bea").SetDataInt("follower_count", 44),
	)
	flushed(t, client)
	items := cap.bodies[0]["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	a := items[1].(map[string]any)
	if a["model"] != "user" || a["id"] != "a" {
		t.Errorf("user item must be a user record: %v", a)
	}
	if data := a["data"].(map[string]any); data["following_count"] != float64(12) {
		t.Errorf("custom data lost: %v", data)
	}
	// Predefined profile fields ride in data, keyed by their own names.
	b := items[2].(map[string]any)
	if data := b["data"].(map[string]any); data["name"] != "Bea" || data["follower_count"] != float64(44) {
		t.Errorf("profile fields must merge into data: %v", data)
	}
}

func TestSetRecordSendsRecordShape(t *testing.T) {
	client, cap := trackServer(t)
	client.SetRecord(NewModel("post", "p2"))
	// A user record takes the same route: /ingest/user expects the flat
	// profile shape and would silently drop `data`.
	client.SetRecord(NewModel("user", "u1").SetDataString("name", "Ada").SetDataString("plan", "team"))
	flushed(t, client)
	if len(cap.bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(cap.bodies))
	}
	if cap.paths[0] != "/ingest/entity" || cap.paths[1] != "/ingest/entity" {
		t.Errorf("both records must post to /ingest/entity, got %v", cap.paths)
	}
	if cap.bodies[0]["model"] != "post" || cap.bodies[1]["model"] != "user" {
		t.Errorf("wrong payloads: %v", cap.bodies)
	}
	data, _ := cap.bodies[1]["data"].(map[string]any)
	if data["name"] != "Ada" || data["plan"] != "team" {
		t.Errorf("user record data must survive: %v", cap.bodies[1]["data"])
	}
}

// Invalid items are dropped at the call and reported to the error handler —
// nothing reaches the network, and the sibling items of a bad one are held
// back with it so a moment is never sent half.
func TestItemValidationAtTheCall(t *testing.T) {
	ec := &errCollector{}
	client := New("http://127.0.0.1:1", "mx_testkey", WithRetries(0), WithErrorHandler(ec.add))
	expect := func(what string, want error) {
		t.Helper()
		errs := ec.all()
		if len(errs) == 0 {
			t.Fatalf("%s: expected a reported drop, got none", what)
		}
		if got := errs[len(errs)-1]; !errors.Is(got, want) {
			t.Errorf("%s: expected %v, got %v", what, want, got)
		}
	}
	client.Track(NewModel("", "p1"))
	expect("key-less model", errNoModelKey)
	// Both halves of NewModel are required — a record is nothing without
	// the id later data and references will find it under.
	client.Track(NewModel("post", ""))
	expect("id-less model", errNoModelId)
	// An id-less user record gets the user-specific error.
	client.Track(NewModel("user", ""))
	expect("id-less user record", errNoUserId)
	client.Track(SetUser(""))
	expect("id-less user", errNoUserId)
	// One bad item drops the whole call.
	client.Track(NewEvent("ok"), NewEvent(""))
	expect("bad sibling", errNoEventKey)
	// A typed nil pointer means a call site built an item and lost it.
	client.Track((*Event)(nil))
	expect("typed nil item", errNilItem)

	before := len(ec.all())
	client.Track()    // no items is a no-op
	client.Track(nil) // an untyped nil item is skipped
	if after := len(ec.all()); after != before {
		t.Errorf("empty calls must not report anything, got %d new errors", after-before)
	}
}

func TestEmptyRelationsAreIgnored(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(
		NewEvent("e").AddUser("", "owner").SetDataModelRelation("post", "").SetDataModelRelation("", "p1"),
		NewModel("post", "p1"),
	)
	flushed(t, client)
	ev := cap.bodies[0]["items"].([]any)[0].(map[string]any)
	if _, present := ev["users"]; present {
		t.Errorf("an id-less related user must be dropped: %v", ev["users"])
	}
	if _, present := ev["models"]; present {
		t.Errorf("a half-named reference must be dropped: %v", ev["models"])
	}
}

func TestIncrementDataTravelsWithAnIncId(t *testing.T) {
	client, cap := trackServer(t)
	client.Track(
		NewEvent("post_liked").SetId("like-1"),
		NewModel("post", "p1").
			IncrementData("like_count", 1).
			IncrementData("like_count", 2). // sums client-side
			IncrementData("score", -0.5).
			SetDataString("kind", "photo"),
	)
	flushed(t, client)
	rec := cap.bodies[0]["items"].([]any)[1].(map[string]any)
	inc, ok := rec["data_inc"].(map[string]any)
	if !ok || inc["like_count"] != float64(3) || inc["score"] != float64(-0.5) {
		t.Fatalf("wrong data_inc: %v", rec["data_inc"])
	}
	if id, _ := rec["inc_id"].(string); id == "" {
		t.Error("an increment must carry an inc_id for server-side dedup")
	}
	if data := rec["data"].(map[string]any); data["kind"] != "photo" {
		t.Errorf("plain data must ride alongside increments: %v", rec["data"])
	}
	// A record without increments carries neither field.
	client.Track(NewEvent("e"), NewModel("post", "p2"))
	flushed(t, client)
	plain := cap.bodies[1]["items"].([]any)[1].(map[string]any)
	if _, present := plain["data_inc"]; present {
		t.Errorf("no increments, no data_inc: %v", plain)
	}
	if _, present := plain["inc_id"]; present {
		t.Errorf("no increments, no inc_id: %v", plain)
	}
}

func TestUserIncrementsRouteToEntityEndpoint(t *testing.T) {
	client, cap := trackServer(t)
	// Increments cannot ride the flat profile shape, so SetUser reroutes.
	client.SetUser(SetUser("u1").SetName("Ada").IncrementData("login_count", 1))
	client.SetUser(SetUser("u1").SetName("Ada"))
	flushed(t, client)
	if cap.paths[0] != "/ingest/entity" || cap.paths[1] != "/ingest/user" {
		t.Fatalf("wrong routing: %v", cap.paths)
	}
	rec := cap.bodies[0]
	if rec["model"] != "user" || rec["id"] != "u1" {
		t.Errorf("increment-carrying update must be a user record: %v", rec)
	}
	if inc, _ := rec["data_inc"].(map[string]any); inc["login_count"] != float64(1) {
		t.Errorf("wrong data_inc: %v", rec["data_inc"])
	}
	if id, _ := rec["inc_id"].(string); id == "" {
		t.Error("user increment must carry an inc_id")
	}
	if data := rec["data"].(map[string]any); data["name"] != "Ada" {
		t.Errorf("profile fields must still merge into data: %v", rec["data"])
	}
}
