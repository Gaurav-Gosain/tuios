package app

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestQuickLookHostHasParam holds the question to an older daemon. Such a
// daemon ignores offset and answers every window with the start of the file, so
// a client that did not ask would join the start of the file to itself. This is
// a wire compatibility test: the e2e suite has no older daemon to run against.
func TestQuickLookHostHasParam(t *testing.T) {
	answer := func(raw string, err error) func(string, string, map[string]any) (json.RawMessage, error) {
		return func(_, verb string, _ map[string]any) (json.RawMessage, error) {
			if verb != "list-verbs" {
				t.Errorf("asked %s, want list-verbs", verb)
			}
			return json.RawMessage(raw), err
		}
	}
	for _, tc := range []struct {
		name string
		raw  string
		err  error
		want bool
	}{
		{"has offset", `{"verbs":[{"verb":"file-preview","params":[{"name":"path"},{"name":"offset"}]}]}`, nil, true},
		{"older", `{"verbs":[{"verb":"file-preview","params":[{"name":"path"},{"name":"max_px"},{"name":"page"}]}]}`, nil, false},
		// A failed question is not an older daemon: the read that follows says what is wrong.
		{"no answer", ``, errors.New("no link"), true},
		{"other verb only", `{"verbs":[{"verb":"file-read","params":[]}]}`, nil, true},
	} {
		if got := quickLookHostHasParam(answer(tc.raw, tc.err), "build", "offset"); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestQuickLookSearchGoesOnFromWhereItStopped holds a search that ran out of its
// time. The e2e suite has no file large enough to run a search out of 5 seconds on
// a shared machine, and a hand made answer is the only way to force that order.
func TestQuickLookSearchGoesOnFromWhereItStopped(t *testing.T) {
	m := keystrokeOS(t, 1, 200, 60)
	target := quickLookTarget{path: "/x.log", name: "x.log"}
	m.quickLook = quickLookState{open: true, target: target, query: "needle", res: &quickLookResult{
		target: target, kind: "text", lines: []string{"a", "b"}, size: 1 << 30, startOff: 0, endOff: 4, more: true, firstLine: 1,
	}}
	ql := &m.quickLook
	m.QuickLookFindMove(true)
	if ql.jump != quickLookSeek || ql.seek.From != 4 {
		t.Fatalf("the first search asks from %d (jump %d), want the end of the held text, 4", ql.seek.From, ql.jump)
	}
	m.applyQuickLookWindow(quickLookWindowMsg{target: target, move: quickLookSeek, seek: ql.seek,
		sought: quickLookSought{Offset: -1, Reached: 500 << 20, Size: 1 << 30}})
	if want := "Searched to 48% in 5 seconds. Press n to go on."; ql.status != want {
		t.Errorf("the panel says %q, want %q", ql.status, want)
	}
	m.QuickLookFindMove(true)
	if ql.jump != quickLookSeek || ql.seek.From != 500<<20 {
		t.Errorf("n asks from %d, want where the search stopped, %d", ql.seek.From, 500<<20)
	}
}
