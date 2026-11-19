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
		{in: "tuios.dev/r/abc-1", where: lookGalleryURL("abc-1"), remote: true},
		{in: "https://tuios.dev/r/abc-1", where: lookGalleryURL("abc-1"), remote: true},
		{in: "https://tuios.dev/r/abc-1.toml", where: lookGalleryURL("abc-1"), remote: true},
		{in: "abc-1", where: lookGalleryURL("abc-1"), remote: true},
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

// The gallery override may leave https only for a literal loopback address.
// A name goes through the resolver, which can answer with any address.
func TestLookGalleryBaseKeepsHTTPSOffLoopback(t *testing.T) {
	for in, ok := range map[string]bool{
		"":                        true,
		"https://example.com":     true,
		"http://127.0.0.1:8080":   true,
		"http://[::1]:8080":       true,
		"http://localhost:8080":   false,
		"http://example.com":      false,
		"http://127.0.0.1.nip.io": false,
		"ftp://127.0.0.1":         false,
	} {
		t.Setenv(lookGalleryEnv, in)
		if _, err := lookGalleryBase(); (err == nil) != ok {
			t.Errorf("%q: error %v, want ok=%v", in, err, ok)
		}
	}
}

// A stranger names a look, and the name reaches the terminal. No character
// that reorders, hides or breaks a line may stay in it.
func TestCleanLookTextDropsFormatCharacters(t *testing.T) {
	for _, r := range []rune{'‮', '⁦', '​', '‍', '\ufeff', '­', ' ', ' ', '؜', '\x1b', '\n'} {
		if got := cleanLookText("a" + string(r) + "b"); got != "a b" {
			t.Errorf("U+%04X: got %q, want %q", r, got, "a b")
		}
	}
}

// lookTooNew decides the refusal and the gallery note. A source build has no
// release version and applies every look.
func TestLookTooNew(t *testing.T) {
	for _, c := range []struct {
		want, running string
		tooNew        bool
	}{
		{"0.14.0", "0.13.3", true},
		{"0.14.0", "v0.13.3", true},
		{"v0.14", "0.13.9", true},
		{"0.14.0", "0.14.0-rc1", true},
		{"0.14.0", "0.14.0", false},
		{"0.14.0", "v0.15.2", false},
		{"0.1.0", "0.13.3", false},
		{"0.14.0", "dev", false},
		{"0.14.0", "dev+abc123", false},
		{"0.14.0", "", false},
		{"", "0.13.3", false},
		{"soon", "0.13.3", false},
	} {
		if got := lookTooNew(c.want, c.running); got != c.tooNew {
			t.Errorf("lookTooNew(%q, %q) = %v, want %v", c.want, c.running, got, c.tooNew)
		}
	}
	if err := checkLookRequires([]byte("look = 1\nrequires = \"0.14.0\"\n"), "0.13.3"); err == nil ||
		err.Error() != "this look needs tuios 0.14.0 or later. You have 0.13.3. Run tuios update first" {
		t.Fatalf("checkLookRequires: %v", err)
	}
}
