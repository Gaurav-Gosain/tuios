package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// resolveLookSource decides what tuios downloads, so it is a security
// boundary: a look comes over https or from a file the person named, and a
// gallery id cannot step out of the gallery path.
func TestResolveLookSource(t *testing.T) {
	files := map[string]bool{"./mine.toml": true, "tokyo": true}
	exists := func(p string) bool { return files[p] }
	cases := []struct {
		in, where, err string
		remote         bool
	}{
		{in: "tuios.dev/r/abc-1", where: lookGallery + "abc-1.toml", remote: true},
		{in: "https://tuios.dev/r/abc-1", where: lookGallery + "abc-1.toml", remote: true},
		{in: "https://tuios.dev/r/abc-1.toml", where: lookGallery + "abc-1.toml", remote: true},
		{in: "abc-1", where: lookGallery + "abc-1.toml", remote: true},
		{in: "https://example.com/a.toml", where: "https://example.com/a.toml", remote: true},
		{in: "./mine.toml", where: "./mine.toml"},
		{in: "tokyo", where: "tokyo"}, // a file of that name wins over the id
		{in: "http://example.com/a.toml", err: "https"},
		{in: "ftp://example.com/a.toml", err: "https"},
		{in: "file:///etc/passwd", err: "https"},
		{in: "tuios.dev/r/../../etc", err: "not a gallery id"},
		{in: "tuios.dev/r/a%2fb", err: "not a gallery id"},
		{in: "../../etc/passwd", err: "not a file"},
		{in: "", err: "give a look"},
	}
	for _, tc := range cases {
		got, err := resolveLookSource(tc.in, exists)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: error %v, want one about %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || got.where != tc.where || got.remote != tc.remote {
			t.Errorf("%q: got %+v, %v; want %s remote=%v", tc.in, got, err, tc.where, tc.remote)
		}
	}
}

// A download may be redirected only to another https URL.
func TestLookRedirectStaysHTTPS(t *testing.T) {
	to := func(s string) *http.Request { u, _ := url.Parse(s); return &http.Request{URL: u} }
	if err := lookHTTP.CheckRedirect(to("http://example.com/a"), []*http.Request{{}}); err == nil {
		t.Error("a redirect to http is followed")
	}
	if err := lookHTTP.CheckRedirect(to("https://example.com/a"), []*http.Request{{}}); err != nil {
		t.Errorf("a redirect to https is refused: %v", err)
	}
}
