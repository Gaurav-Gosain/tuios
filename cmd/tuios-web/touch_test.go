package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The user agents are real, taken from the browsers this has to answer for.
const (
	uaChromeAndroid = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	uaSafariIOS     = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	uaFirefoxAndroi = "Mozilla/5.0 (Android 14; Mobile; rv:127.0) Gecko/127.0 Firefox/127.0"
	uaChromeLinux   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	uaFirefoxLinux  = "Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0"
	uaSafariMac     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15"
)

func TestClientIsTouch(t *testing.T) {
	tests := []struct {
		name  string
		ua    string
		hint  string
		touch bool
	}{
		{name: "chrome on android", ua: uaChromeAndroid, touch: true},
		{name: "safari on ios", ua: uaSafariIOS, touch: true},
		{name: "firefox on android", ua: uaFirefoxAndroi, touch: true},
		{name: "chrome on linux", ua: uaChromeLinux},
		{name: "firefox on linux", ua: uaFirefoxLinux},
		{name: "safari on a mac", ua: uaSafariMac},
		// The hint is an answer where the token is a pattern, so it wins.
		{name: "hint says phone", ua: uaChromeLinux, hint: "?1", touch: true},
		{name: "hint says desktop", ua: uaChromeAndroid, hint: "?0"},
		{name: "nothing at all", ua: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/ws", nil)
			r.Header.Set("User-Agent", tt.ua)
			if tt.hint != "" {
				r.Header.Set("Sec-CH-UA-Mobile", tt.hint)
			}
			if got := clientIsTouch(r); got != tt.touch {
				t.Errorf("clientIsTouch = %v, want %v", got, tt.touch)
			}
		})
	}
}

func TestParseTouchMode(t *testing.T) {
	for _, s := range []string{"auto", "on", "off", "AUTO", " on "} {
		if _, ok := parseTouchMode(s); !ok {
			t.Errorf("parseTouchMode(%q) refused a valid mode", s)
		}
	}
	for _, s := range []string{"", "yes", "true", "touch"} {
		if _, ok := parseTouchMode(s); ok {
			t.Errorf("parseTouchMode(%q) accepted a mode that is not one", s)
		}
	}
}

// The override has to beat the guess in both directions, or a phone that
// reports a desktop user agent and a desktop that reports a phone one are both
// unfixable.
func TestTouchMiddlewareOverrides(t *testing.T) {
	tests := []struct {
		mode  touchMode
		ua    string
		touch bool
	}{
		{mode: touchAuto, ua: uaChromeAndroid, touch: true},
		{mode: touchAuto, ua: uaChromeLinux},
		{mode: touchOn, ua: uaChromeLinux, touch: true},
		{mode: touchOff, ua: uaChromeAndroid},
	}

	for _, tt := range tests {
		var got bool
		next := func(r *http.Request) error {
			got = sessionIsTouch(r.Context())
			return nil
		}
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)
		r.Header.Set("User-Agent", tt.ua)
		if err := touchMiddleware(tt.mode)(next)(r); err != nil {
			t.Fatalf("--touch=%s: %v", tt.mode, err)
		}
		if got != tt.touch {
			t.Errorf("--touch=%s with %q gave touch=%v, want %v", tt.mode, tt.ua, got, tt.touch)
		}
	}
}

// A context that never went through the middleware answers no rather than
// panicking, which is what every caller outside the web server has.
func TestSessionIsTouchWithoutTheMiddleware(t *testing.T) {
	if sessionIsTouch(context.Background()) {
		t.Error("a bare context claimed to be a touch client")
	}
}
