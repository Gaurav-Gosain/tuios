package input

import (
	"encoding/json"
	"testing"
	"time"

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

// TestReviewKeyOnAnOlderDaemonReachesThePane: once a daemon has answered
// review-diff with unknown_verb, ctrl+b v in terminal mode does what an
// unbound key does: v is typed into the pane and nothing more is asked of the
// daemon. (The list-verbs probe that finds the same as the Inbox watch starts
// is tested in the app package.)
func TestReviewKeyOnAnOlderDaemonReachesThePane(t *testing.T) {
	o, typed, _ := reviewInputOS(t)
	var verbs []string
	o.SetInboxVerbCaller(func(verb string, _ map[string]any, _ time.Duration) (json.RawMessage, error) {
		verbs = append(verbs, verb)
		return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: "unknown verb " + verb}
	}, func() string { return "nonce" })
	o.Mode = app.TerminalMode
	k := config.DefaultConfig().Keybindings
	o = pressRun(t, o, k.LeaderKey)
	o = pressRun(t, o, "v")
	if o.ReviewOpen() || len(*typed) != 0 || len(verbs) != 1 {
		t.Fatalf("the first ctrl+b v: open %v, typed %q, verbs %v", o.ReviewOpen(), *typed, verbs)
	}
	o = pressRun(t, o, k.LeaderKey)
	o = pressRun(t, o, "v")
	if o.ReviewOpen() {
		t.Fatal("ctrl+b v opened a review on a daemon that cannot review")
	}
	if len(verbs) != 1 {
		t.Fatalf("ctrl+b v asked an older daemon again: %v", verbs)
	}
	if string(*typed) != "v" {
		t.Fatalf("ctrl+b v on an older daemon typed %q into the pane, want v", *typed)
	}
}

// TestReviewMotionsFollowTheirBindings: the hunk and file motions are the
// review section's keys (#585). Bound elsewhere, the new key moves and the
// old one no longer does.
func TestReviewMotionsFollowTheirBindings(t *testing.T) {
	o := reachReviewOS(t)
	cfg := config.DefaultConfig()
	cfg.Keybindings.Review[config.ActionReviewNextHunk] = []string{"n"}
	cfg.Keybindings.Review[config.ActionReviewNextFile] = []string{"N"}
	o.KeybindRegistry = config.NewKeybindRegistry(cfg)

	before := len(o.RecentActions())
	o = pressRun(t, o, "]")
	o = pressRun(t, o, "}")
	if got := o.RecentActions()[before:]; len(got) != 0 {
		t.Fatalf("] and } still ran %v after they were unbound", got)
	}
	o = pressRun(t, o, "n")
	o = pressRun(t, o, "N")
	got := o.RecentActions()[before:]
	want := []string{config.ActionReviewNextHunk, config.ActionReviewNextFile}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("n then N ran %v, want %v", got, want)
	}
	if !o.ReviewOpen() {
		t.Fatal("the review closed")
	}
	// [ and { keep their defaults.
	o = pressRun(t, o, "[")
	if got := o.RecentActions(); got[len(got)-1] != config.ActionReviewPrevHunk {
		t.Fatalf("[ ran %v, want %s last", got, config.ActionReviewPrevHunk)
	}
}

// TestReviewMotionKeysAreTextInANote: while a note is typed, ] is text for
// the note, not the next-hunk motion.
func TestReviewMotionKeysAreTextInANote(t *testing.T) {
	o := reachReviewOS(t)
	o = pressRun(t, o, "c")
	if !o.ReviewEditing() {
		t.Fatal("c did not open a note on the diff line")
	}
	before := len(o.RecentActions())
	o = pressRun(t, o, "]")
	if got := o.RecentActions()[before:]; len(got) != 0 {
		t.Fatalf("] in a note ran %v", got)
	}
	if !o.ReviewEditing() {
		t.Fatal("] in a note closed the note")
	}
}
