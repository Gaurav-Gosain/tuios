package main

import (
	"context"
	"net/http"

	"github.com/Gaurav-Gosain/sip"
)

// Opening the Inbox from a notification's link.
//
// A push notification links to /inbox?item=ID on tuios-web (notify.web_url).
// sip's client builds its WebSocket address from the page's path and drops
// the query string (see touch.go), so the item cannot ride on the page's URL
// into the session. A cookie does: the browser sends it with the WebSocket
// handshake, which is the one request whose headers reach Go.
//
// So /inbox sets a short-lived cookie naming the item and redirects to the
// page. The handshake middleware reads the cookie into the request context,
// and the session opens the Inbox on that item. The route is behind the same
// Basic Auth as the page. The cookie carries only an item id, which names
// nothing outside this daemon's Inbox, and it expires in a minute, so a later
// visit opens tuios as usual.

// inboxCookie is the cookie /inbox sets.
const inboxCookie = "tuios_inbox"

// inboxCookieAge is how long the cookie lasts, in seconds: long enough for
// the page to load and connect on a slow phone link.
const inboxCookieAge = 60

// inboxCtxKey types the item id in the request context.
type inboxCtxKey struct{}

// validInboxID reports whether s can be an attention item id: a decimal
// counter. Anything else is ignored rather than passed on.
func validInboxID(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// installInboxLink adds the /inbox route and the handshake middleware.
func installInboxLink(cfg *sip.Config) {
	cfg.ConnectMiddleware = append(cfg.ConnectMiddleware, inboxMiddleware())
	cfg.Routes = append(cfg.Routes, inboxRoute())
}

// inboxRoute serves /inbox: it remembers the item and sends the browser to
// the page.
func inboxRoute() sip.Route {
	return sip.Route{Pattern: "/inbox", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.URL.Query().Get("item"); validInboxID(id) {
			http.SetCookie(w, &http.Cookie{
				Name:     inboxCookie,
				Value:    id,
				Path:     "/",
				MaxAge:   inboxCookieAge,
				HttpOnly: true,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
			})
		}
		// Relative, so a page served under a reverse proxy's path prefix
		// goes back to that prefix. http.Redirect would make it absolute
		// from the path this server saw, which has the prefix taken off.
		w.Header().Set("Location", "./")
		w.WriteHeader(http.StatusSeeOther)
	})}
}

// inboxMiddleware reads the cookie at the handshake into the context.
func inboxMiddleware() sip.ConnectMiddleware {
	return func(next sip.ConnectHandler) sip.ConnectHandler {
		return func(r *http.Request) error {
			if c, err := r.Cookie(inboxCookie); err == nil && validInboxID(c.Value) {
				r = r.WithContext(context.WithValue(r.Context(), inboxCtxKey{}, c.Value))
			}
			return next(r)
		}
	}
}

// sessionInboxItem is the item the session should open the Inbox on, or "".
func sessionInboxItem(ctx context.Context) string {
	id, _ := ctx.Value(inboxCtxKey{}).(string)
	return id
}
