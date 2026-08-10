package mixdive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The X-App-Version header follows WithAppVersion exactly: set → sent,
// empty → absent. (Auto-detection is exercised implicitly: test binaries
// carry no vcs settings, so the default is empty here.)
func TestAppVersionHeader(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := r.Header["X-App-Version"]
		if !ok {
			got = append(got, "<absent>")
		} else {
			got = append(got, v[0])
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	with := New(srv.URL, "mx_k", WithAppVersion("abc1234"))
	if err := with.Track(context.Background(), Event{Key: "e"}); err != nil {
		t.Fatalf("track: %v", err)
	}
	without := New(srv.URL, "mx_k", WithAppVersion(""))
	if err := without.Track(context.Background(), Event{Key: "e"}); err != nil {
		t.Fatalf("track: %v", err)
	}
	if len(got) != 2 || got[0] != "abc1234" || got[1] != "<absent>" {
		t.Errorf("X-App-Version per request = %v, want [abc1234 <absent>]", got)
	}
}
