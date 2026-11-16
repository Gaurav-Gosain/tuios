package input

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// reviewInputOS is a client on a daemon session whose daemon answers
// review-diff with one changed line, and records every verb it is sent.
func reviewInputOS(t *testing.T) (*app.OS, *[]byte, *[]string) {
	t.Helper()
	o := reachOS(t)
	o.IsDaemonSession = true
	o.SessionName = "work"
	var typed []byte
	w := o.GetFocusedWindow()
	w.DaemonMode = true
	w.DaemonWriteFunc = func(b []byte) error { typed = append(typed, b...); return nil }
	var verbs []string
	o.SetInboxVerbCaller(func(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
		verbs = append(verbs, verb)
		switch verb {
		case "review-diff":
			return json.Marshal(map[string]any{
				"session": "work", "window": "reach", "base": "main",
				"files": []review.File{{Path: "a.go", Status: "M", Added: 1, Hunks: []review.Hunk{{
					Header: "@@ -1 +1,2 @@", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
					Lines: []review.Line{{Op: "context", Old: 1, New: 1, Text: "package a"}, {Op: "add", New: 2, Text: "var x = 1"}},
				}}}},
				"totals": review.Totals{Files: 1, Added: 1},
			})
		case "review-note":
			return json.Marshal(map[string]any{"notes": []review.Note{}})
		}
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "no"}
	}, func() string { return "nonce" })
	return o, &typed, &verbs
}
