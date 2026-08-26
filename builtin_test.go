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

func TestPageViewConstructor(t *testing.T) {
	client, cap := trackServer(t)
	if err := client.Track(context.Background(),
		PageView("/pricing").SetPropertyString("title", "Pricing")); err != nil {
		t.Fatalf("PageView: %v", err)
	}
	if err := client.Track(context.Background(), PageView("")); err != nil {
		t.Fatalf("PageView empty url: %v", err)
	}
	view := cap.bodies[0]
	if view["event_key"] != "page_view" {
		t.Errorf("wrong page_view payload: %v", view)
	}
	if props, _ := view["properties"].(map[string]any); props["url"] != "/pricing" || props["title"] != "Pricing" {
		t.Errorf("page_view must carry its url and title: %v", view["properties"])
	}
	if _, present := cap.bodies[1]["properties"]; present {
		t.Errorf("an empty url must send no properties: %v", cap.bodies[1]["properties"])
	}
}
