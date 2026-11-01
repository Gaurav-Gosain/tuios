package app

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestQuickLookHostHasWindows holds the question to an older daemon. Such a
// daemon ignores offset and answers every window with the start of the file, so
// a client that did not ask would join the start of the file to itself. This is
// a wire compatibility test: the e2e suite has no older daemon to run against.
func TestQuickLookHostHasWindows(t *testing.T) {
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
		if got := quickLookHostHasWindows(answer(tc.raw, tc.err), "build"); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
