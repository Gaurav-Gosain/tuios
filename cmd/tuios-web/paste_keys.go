package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/Gaurav-Gosain/sip"
)

type appleCtxKey struct{}

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

func appleMiddleware() sip.ConnectMiddleware {
	return func(next sip.ConnectHandler) sip.ConnectHandler {
		return func(r *http.Request) error {
			return next(r.WithContext(context.WithValue(r.Context(), appleCtxKey{}, clientIsApple(r))))
		}
	}
}

func sessionIsApple(ctx context.Context) bool {
	apple, _ := ctx.Value(appleCtxKey{}).(bool)
	return apple
}
