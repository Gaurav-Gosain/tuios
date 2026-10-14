package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/Gaurav-Gosain/sip"
)

// Which keys make the browser paste.
//
// The page keeps the paste keys for the browser: Ctrl+V, Ctrl+Shift+V and
// Shift+Insert, or Cmd+V on a Mac. tuios only says so, in the note it shows
// when something asks it to read the clipboard, and the note has to name the
// keys this browser uses. Like touch, the platform reaches Go only on the
// upgrade request.

// appleCtxKey types the session's answer in the request context.
type appleCtxKey struct{}

// clientIsApple reads the upgrade request for a Mac or an iOS device.
//
// Sec-CH-UA-Platform is a low-entropy client hint Chromium sends without being
// asked, so it is read first. Safari and Firefox send none, and their user
// agent names the platform.
func clientIsApple(r *http.Request) bool {
	switch strings.Trim(strings.TrimSpace(r.Header.Get("Sec-CH-UA-Platform")), `"`) {
	case "macOS", "iOS":
		return true
	case "":
	default:
		return false
	}
	ua := r.UserAgent()
	for _, tok := range []string{"Macintosh", "Mac OS X", "iPhone", "iPad", "iPod"} {
		if strings.Contains(ua, tok) {
			return true
		}
	}
	return false
}

// appleMiddleware answers the question once, at the handshake, and puts the
// answer in the request context, the way touchMiddleware does.
func appleMiddleware() sip.ConnectMiddleware {
	return func(next sip.ConnectHandler) sip.ConnectHandler {
		return func(r *http.Request) error {
			return next(r.WithContext(context.WithValue(r.Context(), appleCtxKey{}, clientIsApple(r))))
		}
	}
}

// sessionIsApple reads back what appleMiddleware decided.
func sessionIsApple(ctx context.Context) bool {
	apple, _ := ctx.Value(appleCtxKey{}).(bool)
	return apple
}
