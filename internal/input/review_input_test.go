package input

import (
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// TestReviewOwnsTheKeyboard: ctrl+b v in terminal mode opens the review of
// the focused pane and types nothing, and while it is open every key is the
// overlay's: j, c and a typed note reach neither the pane nor the Inbox under
// it, a paste goes into the note, and esc closes the note and then the
// overlay.
func TestReviewOwnsTheKeyboard(t *testing.T) {
	o, typed, verbs := reviewInputOS(t)
	o.Mode = app.TerminalMode
	k := config.DefaultConfig().Keybindings
	o = pressRun(t, o, k.LeaderKey)
	o = pressRun(t, o, "v")
	if !o.ReviewOpen() {
		t.Fatalf("ctrl+b v did not open the review (verbs %v, dock %v)", *verbs, o.Notifications)
	}
	if len(*typed) != 0 {
		t.Fatalf("ctrl+b v typed %q into the pane", *typed)
	}
	o.OpenInbox("")
	for _, key := range []string{"j", "j", "c", "h", "i"} {
		o = pressRun(t, o, key)
	}
	if !o.ReviewEditing() {
		t.Fatal("c did not open the note line")
	}
	if _, cmd := HandleInput(tea.PasteMsg{Content: " there\nfriend"}, o); cmd != nil {
		t.Error("a paste into the note line ran a command")
	}
	o = pressRun(t, o, "enter")
	if len(*typed) != 0 {
		t.Errorf("keys in the review reached the pane: %q", *typed)
	}
	if !o.ShowInbox {
		t.Error("a key in the review reached the Inbox under it")
	}
	found := false
	for _, v := range *verbs {
		found = found || v == "review-note"
	}
	if !found {
		t.Errorf("enter on the note line sent nothing: %v", *verbs)
	}
	o = pressRun(t, o, "esc")
	if o.ReviewOpen() {
		t.Error("esc did not close the review")
	}
	if !o.ShowInbox {
		t.Error("closing the review closed the Inbox under it")
	}
}
