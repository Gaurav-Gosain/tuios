package main

import (
	"context"
	"net/http"

	"github.com/Gaurav-Gosain/sip"
)

const inboxCookie = "tuios_inbox"

const inboxCookieAge = 60

type inboxCtxKey struct{}

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

func installInboxLink(cfg *sip.Config) {
	cfg.ConnectMiddleware = append(cfg.ConnectMiddleware, inboxMiddleware())
	cfg.Routes = append(cfg.Routes, inboxRoute())
}

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
		w.Header().Set("Location", "./")
		w.WriteHeader(http.StatusSeeOther)
	})}
}

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

func sessionInboxItem(ctx context.Context) string {
	id, _ := ctx.Value(inboxCtxKey{}).(string)
	return id
}
