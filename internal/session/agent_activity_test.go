package session

import (
	"slices"
	"strings"
	"testing"
)

// These tests cover the activity ring: its text is cleaned and cut before it
// is stored, and a report for a closed window leaves no ring behind.

// TestActivityTextIsCleaned: activity text is the agent's, so it is kept to
// one line with no control characters and likely secrets masked.
func TestActivityTextIsCleaned(t *testing.T) {
	e := activityEntryOf(&AgentActivityReport{
		Event:  ActivityPrompt,
		Tool:   "Ba\x1bsh",
		Target: "export API_TOKEN=abc123 &&\n make",
		Text:   "first \x1b[31mline\nsecond line",
		Files:  []string{"a.go", "a.go", "", "b.go"},
	})
	if e.Text != "first [31mline" {
		t.Errorf("text = %q", e.Text)
	}
	if strings.Contains(e.Target, "abc123") || strings.Contains(e.Target, "\n") {
		t.Errorf("target = %q, want one line with the token masked", e.Target)
	}
	if e.Tool != "Bash" {
		t.Errorf("tool = %q", e.Tool)
	}
	if !slices.Equal(e.Files, []string{"a.go", "b.go"}) {
		t.Errorf("files = %v", e.Files)
	}
}

// TestShellCommandTargetIsCut: a shell command line reaches the ring cut to
// activityTextMax like every other target, although the shell's own record of
// it keeps up to shellCmdlineMax bytes, and a secret in it is masked.
func TestShellCommandTargetIsCut(t *testing.T) {
	store := newActivityStore(nil)
	s := &Session{ID: "sid", Name: "work"}
	store.add(s.ID, s.Name, "w", AgentActivityEntry{Kind: ActivityPrompt, Text: "go"}, true)
	long := "API_TOKEN=abc123def go test " + strings.Repeat("./pkg/x ", shellCmdlineMax/8)
	store.noteSessionEvent(s, SessionEvent{Type: EventCommandFinished, Window: "w", Cmdline: long, ExitCode: intPtr(0)})
	got, _, _ := store.read(s.ID, "w")
	if len(got) != 2 {
		t.Fatalf("ring = %+v", got)
	}
	if n := len(got[1].Target); n == 0 || n > activityTextMax {
		t.Errorf("command target is %d bytes, want 1 to %d", n, activityTextMax)
	}
	if strings.Contains(got[1].Target, "abc123def") {
		t.Errorf("command target kept a secret: %q", got[1].Target)
	}
}

// TestActivityForAClosedWindowLeavesNoRing: the window a report named can
// close between the report resolving it and its activity being added. The
// sink has already forgotten the window's ring by then, so the add must not
// leave a new one behind.
func TestActivityForAClosedWindowLeavesNoRing(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	d.recordAgentActivity(sess, "closed-window", &AgentActivityReport{Event: ActivityTool, Tool: "Bash", Target: "make"}, AgentStateWorking)
	if d.activity.has(sess.ID, "closed-window") {
		t.Error("activity for a closed window left a ring")
	}
	id := sess.GetState().Windows[0].ID
	d.recordAgentActivity(sess, id, &AgentActivityReport{Event: ActivityTool, Tool: "Bash", Target: "make"}, AgentStateWorking)
	if !d.activity.has(sess.ID, id) {
		t.Error("activity for a live window left no ring")
	}
}
