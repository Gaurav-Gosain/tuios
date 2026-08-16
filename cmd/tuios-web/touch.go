package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/Gaurav-Gosain/sip"
)

// Deciding whether the far end is a finger.
//
// sip does not put this on the wire. Its client knows the answer exactly (it
// calls detectTouch to decide whether to install the key bar and the touch
// mouse at all) and then throws it away: the only messages a client sends are
// the resize, carrying cols, rows and the canvas pixel size, and input bytes,
// and the WebSocket URL is rebuilt from the page's path with the query string
// dropped, so even ?mobile=1 does not survive. The upgrade request's headers
// are the only place any property of the browser reaches Go.
//
// So this is a guess, and it is named one. The client hint is exact where it is
// sent, the user-agent tokens are the usual approximation, and iPadOS Safari
// asking for the desktop site is the case that has no answer at all, which is
// why --touch overrides both.

// touchMode is what the operator asked for.
type touchMode string

const (
	touchAuto touchMode = "auto"
	touchOn   touchMode = "on"
	touchOff  touchMode = "off"
)

// parseTouchMode validates the --touch value.
func parseTouchMode(s string) (touchMode, bool) {
	switch m := touchMode(strings.ToLower(strings.TrimSpace(s))); m {
	case touchAuto, touchOn, touchOff:
		return m, true
	default:
		return "", false
	}
}

// touchCtxKey types the session's answer in the request context.
type touchCtxKey struct{}

// touchTokens are the user-agent substrings that mean a touch screen, matched
// case-insensitively. "Mobile" is last because it is the weakest: Firefox on
// Android is the browser that carries it and nothing else useful.
var touchTokens = []string{"Android", "iPhone", "iPad", "iPod", "Silk/", "Mobile"}

// clientIsTouch reads the upgrade request for a touch screen.
//
// Sec-CH-UA-Mobile is a low-entropy client hint Chromium sends without being
// asked, and "?1" is its answer for a phone. It is checked first because it is
// the only one of the two that is an answer rather than a pattern match.
func clientIsTouch(r *http.Request) bool {
	switch strings.TrimSpace(r.Header.Get("Sec-CH-UA-Mobile")) {
	case "?1":
		return true
	case "?0":
		return false
	}
	ua := r.UserAgent()
	for _, tok := range touchTokens {
		if strings.Contains(strings.ToLower(ua), strings.ToLower(tok)) {
			return true
		}
	}
	return false
}

// touchMiddleware answers the question once, at the handshake, and puts the
// answer in the request context.
//
// sip carries that context into the session (the terminal handler keeps the
// request as the chain last passed it and derives the session context from it),
// which is what lets a per-session flag exist at all: one server holds several
// clients and a phone attaching must not change what a desktop beside it can
// hit.
func touchMiddleware(mode touchMode) sip.ConnectMiddleware {
	return func(next sip.ConnectHandler) sip.ConnectHandler {
		return func(r *http.Request) error {
			touch := mode == touchOn
			if mode == touchAuto {
				touch = clientIsTouch(r)
			}
			return next(r.WithContext(context.WithValue(r.Context(), touchCtxKey{}, touch)))
		}
	}
}

// sessionIsTouch reads back what touchMiddleware decided. A session with no
// answer is not a touch client, which is what a caller outside the web server
// gets.
func sessionIsTouch(ctx context.Context) bool {
	touch, _ := ctx.Value(touchCtxKey{}).(bool)
	return touch
}
