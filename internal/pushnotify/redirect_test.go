package pushnotify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRedirectStaysOnTheHost is a security boundary: a provider that answers
// with a redirect to another host must not get the message, or the token in
// it, sent there. net/http drops the Authorization header on such a redirect,
// but a 307 or 308 sends the body again, and Pushover's token is in the body.
// A redirect on the same host is still followed, so the refusal is about the
// host and not about redirects.
func TestRedirectStaysOnTheHost(t *testing.T) {
	var elsewhere atomic.Int32
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		body, _ := io.ReadAll(r.Body)
		leaked.Store(string(body))
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	var landed atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
		case "/here":
			http.Redirect(w, r, "/landed", http.StatusPermanentRedirect)
		case "/landed":
			landed.Add(1)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer origin.Close()

	send := func(path string) error {
		n := &config.NotifyConfig{Pushover: &config.PushoverConfig{
			URL: origin.URL + path, User: "user-key", Token: "app-token-secret",
		}}
		// allowHTTP: the test servers are plain http. The host check must
		// hold on its own.
		return Providers(n)[0].Send(context.Background(), NewClient(true), Message{Title: "t"})
	}

	err := send("/away")
	if err == nil || !strings.Contains(err.Error(), "another host") {
		t.Errorf("a redirect to another host = %v, want a refusal that says so", err)
	}
	if n := elsewhere.Load(); n != 0 {
		t.Fatalf("the other host got %d requests, body %q", n, leaked.Load())
	}

	if err := send("/here"); err != nil {
		t.Errorf("a redirect on the same host = %v, want it followed", err)
	}
	if landed.Load() != 1 {
		t.Errorf("the same-host redirect never landed")
	}
}
