package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestHostsSaysWhichHostIsPolled is the version skew on the command line: a
// host whose tuios is too old to stream its agents is named, with what to
// update, and a streamed host is not.
//
// Negative control: without the polling loop in printHostList neither note is
// printed.
func TestHostsSaysWhichHostIsPolled(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"total": 2,
		"hosts": []map[string]any{
			{"host": "build", "addr": "build", "status": "up", "events": "live"},
			{"host": "old", "addr": "old", "status": "up", "events": "polling", "events_note": "The tuios on this host has no Inbox. Update tuios on the host."},
		},
	})
	var out bytes.Buffer
	if err := printHostList(&out, raw); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "old: The tuios on this host has no Inbox. Update tuios on the host.") {
		t.Errorf("the polled host is not named:\n%s", got)
	}
	if strings.Contains(got, "build: ") {
		t.Errorf("a streamed host got a note:\n%s", got)
	}
}

// TestAllSessionsNamesEachRowsSession: list-agents --all-sessions says which
// session each pane is in, in the form -s and -w take.
func TestAllSessionsNamesEachRowsSession(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"all_sessions": true,
		"total":        2,
		"agents": []map[string]any{
			{"session": "alpha", "window_id": "0123456789", "name": "claude", "state": "working"},
			{"session": "beta", "window_id": "abcdef0123", "name": "codex", "state": "done"},
		},
	})
	var out bytes.Buffer
	if err := printAgentList(&out, raw, false, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alpha/claude", "beta/codex"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the listing lacks %q:\n%s", want, out.String())
		}
	}
}
