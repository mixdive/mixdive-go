package mixdive

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The X-App-Version header follows WithAppVersion exactly: set → sent,
// empty → absent. (Auto-detection is exercised implicitly: test binaries
// carry no vcs settings, so the default is empty here.)
func TestAppVersionHeader(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := r.Header["X-App-Version"]
		mu.Lock()
		if !ok {
			got = append(got, "<absent>")
		} else {
			got = append(got, v[0])
		}
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	with := New(srv.URL, "mx_k", WithAppVersion("abc1234"))
	with.Track(NewEvent("e"))
	flushed(t, with)
	without := New(srv.URL, "mx_k", WithAppVersion(""))
	without.Track(NewEvent("e"))
	flushed(t, without)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "abc1234" || got[1] != "<absent>" {
		t.Errorf("X-App-Version per request = %v, want [abc1234 <absent>]", got)
	}
}
