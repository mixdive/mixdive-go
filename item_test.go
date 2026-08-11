package mixdive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// trackServer serves /ingest/track and returns the capture behind it.
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
	return New(srv.URL, "mx_testkey"), cap
}

func TestTrackSendsMixedItemsInOneCall(t *testing.T) {
	client, cap := trackServer(t)
	err := client.Track(context.Background(),
		NewEvent("post_created").SetId("p1").SetUser("u_author"),
		NewModel("post", "p1").SetDataString("kind", "photo"),
	)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
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
	if err := client.Track(context.Background(), NewEvent("app_opened")); err != nil {
		t.Fatalf("Track: %v", err)
	}
	// The bare event endpoint requires event_key; the alias rides along.
	if got := cap.bodies[0]; got["event_key"] != "app_opened" {
		t.Errorf("single event must post the contract's event_key: %v", got)
	}
}

func TestTrackCarriesUsersWithRolesAndRefs(t *testing.T) {
	client, cap := trackServer(t)
	err := client.Track(context.Background(),
		NewEvent("post_liked").
			SetId("like-u9-p1").
			AddUser("u9", "").
			AddUser("u_author", "owner").
			SetRelation("post", "p1"),
		NewModel("post", "p1").SetDataInt("like_count", 313),
	)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
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

func TestUserItemTravelsInsideTrack(t *testing.T) {
	client, cap := trackServer(t)
	err := client.Track(context.Background(),
		NewEvent("user_followed").SetId("f-a-b").SetUser("a"),
		SetUser("a").SetDataInt("following_count", 12),
		SetUser("b").SetName("Bea").SetDataInt("follower_count", 44),
	)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
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
	if err := client.SetRecord(context.Background(), NewModel("post", "p2")); err != nil {
		t.Fatalf("SetRecord: %v", err)
	}
	// A user record takes the same route: /ingest/user expects the flat
	// profile shape and would silently drop `data`.
	if err := client.SetRecord(context.Background(),
		NewModel("user", "u1").SetDataString("name", "Ada").SetDataString("plan", "team")); err != nil {
		t.Fatalf("SetRecord user: %v", err)
	}
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

func TestItemValidationBeforeNetwork(t *testing.T) {
	client := New("http://127.0.0.1:1", "mx_testkey", WithRetries(0))
	ctx := context.Background()
	if err := client.Track(ctx, NewModel("", "p1")); !errors.Is(err, errNoModelKey) {
		t.Errorf("expected errNoModelKey, got %v", err)
	}
	// Both halves of NewModel are required — a record is nothing without
	// the id later data and references will find it under.
	if err := client.Track(ctx, NewModel("post", "")); !errors.Is(err, errNoModelId) {
		t.Errorf("expected errNoModelId, got %v", err)
	}
	// An id-less user record gets the user-specific error.
	if err := client.Track(ctx, NewModel("user", "")); !errors.Is(err, errNoUserId) {
		t.Errorf("expected errNoUserId for an id-less user record, got %v", err)
	}
	if err := client.Track(ctx, SetUser("")); !errors.Is(err, errNoUserId) {
		t.Errorf("expected errNoUserId, got %v", err)
	}
	// One bad item fails the call before anything is sent.
	if err := client.Track(ctx, NewEvent("ok"), NewEvent("")); !errors.Is(err, errNoEventKey) {
		t.Errorf("expected the bad sibling to fail the call, got %v", err)
	}
	// A typed nil pointer means a call site built an item and lost it.
	if err := client.Track(ctx, (*Event)(nil)); !errors.Is(err, errNilItem) {
		t.Errorf("expected errNilItem for a nil *Event, got %v", err)
	}
	if err := client.Track(ctx); err != nil {
		t.Errorf("no items must be a no-op, got %v", err)
	}
	if err := client.Track(ctx, nil); err != nil {
		t.Errorf("an untyped nil item must be skipped, got %v", err)
	}
}

func TestEmptyRelationsAreIgnored(t *testing.T) {
	client, cap := trackServer(t)
	err := client.Track(context.Background(),
		NewEvent("e").AddUser("", "owner").SetRelation("post", "").SetRelation("", "p1"),
		NewModel("post", "p1"),
	)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
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
	err := client.Track(context.Background(),
		NewEvent("post_liked").SetId("like-1"),
		NewModel("post", "p1").
			IncrementData("like_count", 1).
			IncrementData("like_count", 2). // sums client-side
			IncrementData("score", -0.5).
			SetDataString("kind", "photo"),
	)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
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
	if err := client.Track(context.Background(), NewEvent("e"), NewModel("post", "p2")); err != nil {
		t.Fatalf("Track: %v", err)
	}
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
	if err := client.SetUser(context.Background(),
		SetUser("u1").SetName("Ada").IncrementData("login_count", 1)); err != nil {
		t.Fatalf("SetUser with increments: %v", err)
	}
	if err := client.SetUser(context.Background(), SetUser("u1").SetName("Ada")); err != nil {
		t.Fatalf("SetUser plain: %v", err)
	}
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
