package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The target grammar as the flags see it, and what the CLI prints for mail
// that came from another machine. The grammar itself is pinned in the
// federation package; this holds the flag-level rules: which of -s and -w
// wins, when they must agree, and that local folds to this machine.

func TestResolveTargetFlags(t *testing.T) {
	cases := []struct {
		s, w               string
		host, sess, window string
		wantErr            bool
	}{
		{"", "", "", "", "", false},
		{"api", "0", "", "api", "0", false},
		{"build:api", "0", "build", "api", "0", false},
		{"build:", "editor", "build", "", "editor", false},
		{"", "build:api:0", "build", "api", "0", false},
		{"build:api", "build:api:0", "build", "api", "0", false},
		{"local:api", "0", "", "api", "0", false},
		{"", "local::https://x", "", "", "https://x", false},
		// A plain window title that only looks qualified stays a window here.
		{"", "https://example.com", "", "", "https://example.com", false},
		// Disagreement is refused rather than guessed.
		{"api", "build:api:0", "", "", "", true},
		{"build:api", "other:api:0", "", "", "", true},
		{"build:api", "build:ci:0", "", "", "", true},
	}
	for _, c := range cases {
		host, sess, window, err := resolveTarget(c.s, c.w)
		if (err != nil) != c.wantErr {
			t.Errorf("ASSERTION: resolveTarget(%q, %q) err = %v, want error %v", c.s, c.w, err, c.wantErr)
			continue
		}
		if err == nil && (host != c.host || sess != c.sess || window != c.window) {
			t.Errorf("ASSERTION: resolveTarget(%q, %q) = %q %q %q, want %q %q %q", c.s, c.w, host, sess, window, c.host, c.sess, c.window)
		}
	}
}

func TestPrintedMailSaysWhereItCameFrom(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"messages": []map[string]any{
			{"id": 1, "kind": "message", "from_label": "ORCHESTRATOR", "text": "ship it?", "thread_id": 1,
				"origin": "link", "origin_host": "laptop"},
			{"id": 2, "kind": "message", "from": "cccccccc3333", "from_label": "build", "text": "yes", "thread_id": 1},
		},
		"total": 2, "unread": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := printAgentMessages(&buf, raw, "build"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "from ORCHESTRATOR on laptop, arrived over a link") {
		t.Errorf("ASSERTION: a message from another machine is printed without its origin:\n%s", out)
	}
	if !strings.Contains(out, "untrusted content from ORCHESTRATOR on laptop, arrived over a link, in the ring on build") {
		t.Errorf("ASSERTION: the fence does not name the machine the message came from and the machine the ring is on:\n%s", out)
	}
	if !strings.Contains(out, "2 message(s) on build") {
		t.Errorf("ASSERTION: the summary does not say which host answered:\n%s", out)
	}
	// The local message in the same ring is not marked as remote.
	if strings.Contains(out, "build (cccccccc) on") {
		t.Errorf("a message from build's own pane is printed as remote:\n%s", out)
	}
}

// TestPrintedMailCannotReachTheTerminal is the fence at the last step: a body
// another machine wrote goes through this printer to a terminal, and a
// terminal acts on escape sequences. None survive.
func TestPrintedMailCannotReachTheTerminal(t *testing.T) {
	hostile := "\x1b]52;c;ZXZpbA==\x07\x1b[2J\x1b[31mred\x1b[0m\r\n\x9bnext line\ttab"
	raw, err := json.Marshal(map[string]any{
		"messages": []map[string]any{
			{"id": 1, "kind": "message", "from_label": "at\x1btacker", "subject": "\x1b[1mURGENT", "text": hostile,
				"origin": "link", "origin_host": "ev\x1bil",
				"attachments": []map[string]any{{"kind": "file", "path": "/tmp/x\x1b[2J", "media_type": "text/plain\x07", "bytes": 1}}},
		},
		"total": 1, "unread": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := printAgentMessages(&buf, raw, ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, bad := range []string{"\x1b", "\x07", "\x9b", "\r"} {
		if strings.Contains(out, bad) {
			t.Fatalf("ASSERTION: a control byte %q from a message reached the printed output:\n%q", bad, out)
		}
	}
	for _, want := range []string{"]52;c;ZXZpbA==", "red", "next line\ttab", "URGENT", "attacker", "evil"} {
		if !strings.Contains(out, want) {
			t.Errorf("the printable part %q was lost:\n%q", want, out)
		}
	}

	// The agent listing prints names other programs chose, and gets the
	// same treatment.
	raw, _ = json.Marshal(map[string]any{
		"agents": []map[string]any{{"window_id": "abcdefgh1234", "name": "ed\x1b[2Jitor", "state": "idle", "message": "\x1b]0;x\x07note"}},
		"total":  1,
	})
	buf.Reset()
	if err := printAgentList(&buf, raw, false, ""); err != nil {
		t.Fatal(err)
	}
	// The table itself may carry styling escapes, so the check is for the
	// sequences the names carried, not for any escape at all.
	for _, bad := range []string{"\x1b[2J", "\x1b]0;", "\x07"} {
		if strings.Contains(buf.String(), bad) {
			t.Fatalf("ASSERTION: the sequence %q from a window name reached the agent listing:\n%q", bad, buf.String())
		}
	}
}
