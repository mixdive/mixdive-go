package mixdive

import (
	"context"
	"testing"
)

func TestBuiltinEventConstructors(t *testing.T) {
	client, cap := trackServer(t)
	if err := client.Track(context.Background(), Login("google").SetUser("u1")); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := client.Track(context.Background(), SignUp("").SetId("sign-up-u2").SetUser("u2")); err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	login := cap.bodies[0]
	if login["event_key"] != "login" || login["user_id"] != "u1" {
		t.Errorf("wrong login payload: %v", login)
	}
	if props, _ := login["properties"].(map[string]any); props["method"] != "google" {
		t.Errorf("login must carry its method parameter: %v", login["properties"])
	}
	signup := cap.bodies[1]
	if signup["event_key"] != "sign_up" || signup["id"] != "sign-up-u2" {
		t.Errorf("wrong sign_up payload: %v", signup)
	}
	if _, present := signup["properties"]; present {
		t.Errorf("an empty method must send no properties: %v", signup["properties"])
	}
}
