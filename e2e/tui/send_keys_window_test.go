package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSendKeysToANamedWindowWithAClientAttached is what an agent in a pane
// does to drive two other windows while the person watches: open two windows
// without taking the focus, then send arrows to one of them by name and to the
// other by id. Each window runs a program whose terminal echoes what it reads
// with control bytes made visible, so ESC [ B shows as ^[[B.
//
// With a client attached, send-keys used to hand every parsed key to the
// client, which read them as the person's keys and ignored the window: in
// window-management mode Down moved the selection, and in terminal mode it
// went to the focused window, usually the agent's own. The command exited 0
// either way. Negative control: route window-targeted keys to the client
// again in verbSendKeys and the first wait below times out with the pager
// window unchanged.
func TestSendKeysToANamedWindowWithAClientAttached(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	echo := []string{"/bin/sh", "-c", "stty icanon echo echoctl; printf 'ready\\n'; cat >/dev/null"}

	var ids []string
	for _, name := range []string{"pager-a", "pager-b"} {
		args := append([]string{"new-window", "-s", sess, "--no-focus", "--print-id", name, "--"}, echo...)
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		id := strings.TrimSpace(out)
		if len(id) != 36 {
			t.Fatalf("new-window --print-id printed %q, want a full window id", out)
		}
		ids = append(ids, id)
		if out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", name, "--pattern", "ready", "--timeout", "10000"); err != nil {
			t.Fatalf("window %s never started: %v\n%s", name, err, out)
		}
	}
	focusedBefore := focusedWindowID(t, base, sess)
	before := capture(t, base, sess, "pager-b")

	out, err := tuiosCLI(t, base, "send-keys", "-s", sess, "-w", "pager-a", "Down", "--repeat", "3")
	if err != nil {
		t.Fatalf("send-keys to pager-a: %v\n%s", err, out)
	}
	if want := "sent 3 keys to window pager-a (" + ids[0][:8] + ")"; strings.TrimSpace(out) != want {
		t.Errorf("send-keys printed %q, want %q", strings.TrimSpace(out), want)
	}
	waitCapture(t, base, sess, "pager-a", "^[[B^[[B^[[B")

	if after := capture(t, base, sess, "pager-b"); after != before {
		t.Errorf("pager-b changed when only pager-a was sent keys:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := focusedWindowID(t, base, sess); got != focusedBefore {
		t.Errorf("the focus moved from %s to %s: the keys reached the window manager", focusedBefore, got)
	}

	// The other window by id, with a page key.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", sess, "-w", ids[1], "PageDown"); err != nil {
		t.Fatalf("send-keys to pager-b: %v\n%s", err, out)
	}
	waitCapture(t, base, sess, "pager-b", "^[[6~")
	if a := capture(t, base, sess, "pager-a"); strings.Contains(a, "^[[6~") {
		t.Errorf("pager-a read the page key sent to pager-b:\n%s", a)
	}
	alive(t, term, "after sending keys to named windows")
}

// capture is one window's visible screen.
func capture(t *testing.T, base, sess, window string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", window)
	if err != nil {
		t.Fatalf("capture-pane %s: %v\n%s", window, err, out)
	}
	return out
}

// focusedWindowID is the id of the session's focused window.
func focusedWindowID(t *testing.T, base, sess string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var list struct {
		Focused string `json:"focused_window_id"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("list-windows --json printed %q: %v", out, err)
	}
	return list.Focused
}
