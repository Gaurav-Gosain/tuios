package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/Gaurav-Gosain/sip"
)

type touchMode string

const (
	touchAuto touchMode = "auto"
	touchOn   touchMode = "on"
	touchOff  touchMode = "off"
)

func parseTouchMode(s string) (touchMode, bool) {
	switch m := touchMode(strings.ToLower(strings.TrimSpace(s))); m {
	case touchAuto, touchOn, touchOff:
		return m, true
	default:
		return "", false
	}
}

type touchCtxKey struct{}

var touchTokens = []string{"Android", "iPhone", "iPad", "iPod", "Silk/", "Mobile"}

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

func sessionIsTouch(ctx context.Context) bool {
	touch, _ := ctx.Value(touchCtxKey{}).(bool)
	return touch
}
