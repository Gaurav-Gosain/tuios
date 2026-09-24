package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestQueueCommandRoundTrip drives tuios queue, queue ls and queue rm against
// a real daemon: a message queued for a working agent is listed as waiting and
// by shell, text after -- that reads ls is queued rather than taken as the
// subcommand, and rm --all drops what the shell queued.
func TestQueueCommandRoundTrip(t *testing.T) {
	c := startSubscribeDaemon(t)
	if _, err := c.Call("new-session", map[string]any{"name": "work"}); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	windows, err := c.Call("list-windows", map[string]any{"session": "work"})
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var list struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(windows, &list); err != nil || len(list.Windows) == 0 {
		t.Fatalf("list-windows = %s (%v), want a window", windows, err)
	}
	pane := list.Windows[0].ID
	if _, err := c.Call("set-agent-state", map[string]any{"session": "work", "window": pane, "state": "working"}); err != nil {
		t.Fatalf("set-agent-state: %v", err)
	}

	run := func(args ...string) {
		t.Helper()
		root := newRootCommand()
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("tuios %s: %v", strings.Join(args, " "), err)
		}
	}
	run("queue", "-s", "work", "-w", pane, "make", "the", "backoff", "configurable")
	run("queue", "-s", "work", "-w", pane, "--", "ls")

	var out bytes.Buffer
	if err := runQueueList(&out, "work", "", false); err != nil {
		t.Fatalf("queue ls: %v", err)
	}
	text := out.String()
	for _, want := range []string{"ID", "waiting", "shell", "make the backoff configurable"} {
		if !strings.Contains(text, want) {
			t.Errorf("queue ls does not show %q:\n%s", want, text)
		}
	}
	if lines := strings.Split(strings.TrimSpace(text), "\n"); len(lines) != 3 || !strings.HasSuffix(strings.TrimSpace(lines[2]), "ls") {
		t.Errorf("queue ls = %q, want a header and two entries, the second reading ls", text)
	}

	run("queue", "rm", "-s", "work", "-w", pane, "--all")
	out.Reset()
	if err := runQueueList(&out, "work", pane, false); err != nil {
		t.Fatalf("queue ls: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing is queued") {
		t.Errorf("after rm --all, queue ls = %q", out.String())
	}
}

func TestDescribeQueued(t *testing.T) {
	for _, tc := range []struct {
		window     string
		position   int
		delivering bool
		want       string
	}{
		{"build", 1, true, "Queued q1 for build. The agent is at rest, so it is typed now."},
		{"build", 1, false, "Queued q1 for build. It is typed when the agent comes to rest."},
		{"", 3, false, "Queued q1 for the focused pane, number 3 in line."},
	} {
		if got := describeQueued("q1", tc.window, tc.position, tc.delivering); !strings.HasPrefix(got, tc.want) {
			t.Errorf("describeQueued(%q, %d, %v) = %q, want %q", tc.window, tc.position, tc.delivering, got, tc.want)
		}
	}
}
