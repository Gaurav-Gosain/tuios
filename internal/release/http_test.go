//go:build !js

package release

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A redirect that leaves the host must not carry the API token: a release
// asset redirects to another host, which is not GitHub's API.
func TestRedirectToAnotherHostDropsTheToken(t *testing.T) {
	var got string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "asset")
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer first.Close()
	resp, err := httpGet(context.Background(), first.URL, "application/octet-stream", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if b, _ := io.ReadAll(resp.Body); string(b) != "asset" {
		t.Fatalf("body %q, want the asset", b)
	}
	if got != "" {
		t.Fatalf("the redirect target got Authorization %q, want none", got)
	}
}

// A download that began on https never falls back to plain http.
func TestHTTPSDownloadRefusesAPlainRedirect(t *testing.T) {
	https, _ := http.NewRequest(http.MethodGet, "https://example.test/a", nil)
	plain, _ := http.NewRequest(http.MethodGet, "http://example.test/b", nil)
	if err := checkRedirect(plain, []*http.Request{https}); err == nil {
		t.Fatal("a redirect from https to http was followed")
	}
	if err := checkRedirect(https, []*http.Request{https}); err != nil {
		t.Fatalf("a redirect to https was refused: %v", err)
	}
}
