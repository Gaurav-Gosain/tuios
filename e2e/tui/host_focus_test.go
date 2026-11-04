package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Host terminal focus. The client asks its terminal for focus events (DECSET
// 1004); the harness plays the terminal and sends them. What the test reads is
// what a person or a script can read: session-info's host_focus, the OSC 9
// notification on the host stream, and the file an after-agent-state hook
// writes.
//
// How this could pass wrongly, written down first:
//   - host_focus might read "focused" from a default rather than from the
//     event, so it is read as "unknown" before any event is sent;
//   - an alert might be missing for a reason other than focus (settle, quiet
//     hours, the state not alerting), so the same transition is shown to alert
//     once the terminal loses focus, in the same fixture (the positive half);
//   - an alert that arrives late could land after the check, so the held case
//     waits well past the settle window, which is zero here, before it looks;
//   - the stream holds the alerts of earlier steps, so every check reads only
//     what arrived after its own mark.

// sessionHostFocus reads host_focus from session-info.
func sessionHostFocus(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "session-info", "-s", session, "--json")
	if err != nil {
		t.Fatalf("session-info: %v\n%s", err, out)
	}
	var res struct {
		HostFocus string `json:"host_focus"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("session-info --json did not print JSON: %v\n%s", err, out)
	}
	return res.HostFocus
}

// waitHostFocus polls session-info until host_focus is want.
func waitHostFocus(t *testing.T, base, session, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		if got = sessionHostFocus(t, base, session); got == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("host_focus is %q, want %q", got, want)
}

// waitNotified waits for an OSC 9 notification holding want after mark.
func waitNotified(t *testing.T, term *tuitest.Terminal, stream *hostStream, mark int, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		for _, n := range notifySince(stream, mark) {
			if strings.Contains(n, want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notification with %q: %q\n%s", want, notifySince(stream, mark), term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// notifySince returns the OSC 9 notifications on the host stream after mark.
func notifySince(stream *hostStream, mark int) []string {
	b := stream.bytes()
	if mark > len(b) {
		mark = len(b)
	}
	var out []string
	rest := b[mark:]
	for {
		i := bytes.Index(rest, []byte("\x1b]9;"))
		if i < 0 {
			return out
		}
		rest = rest[i+4:]
		end := bytes.IndexAny(rest, "\x07\x1b")
		if end < 0 {
			end = len(rest)
		}
		out = append(out, string(rest[:end]))
	}
}

// TestHostFocusHoldsAlertsForThePaneYouAreLookingAt: with suppress_focused on,
// which is the default, a transition on the focused pane raises nothing while
// the terminal has focus, and raises the notification and the daemon's hook
// once the terminal has lost it. A turn alerts once, and a client attached
// after it does not alert for the same turn again.
//
// Negative controls: with the tea.FocusMsg and tea.BlurMsg cases in update.go
// sending nothing, host_focus stays "unknown" and the first wait fails. With
// HostLooking cut from considerAgentAlert's suppress check, the notification
// is held after the blur too and the positive half fails. With the silent
// baseline cut from considerAgentAlert, the client attached after the done
// alerts for it a second time.
func TestHostFocusHoldsAlertsForThePaneYouAreLookingAt(t *testing.T) {
	const session = "e2e-focus"
	base := t.TempDir()
	killDaemon(t, base)
	hookOut := filepath.Join(base, "hook.log")
	writeConfig(t, base, "[notifications.agent]\nsettle_seconds = 0\n\n[hooks]\nafter-agent-state = \"echo $TUIOS_AGENT_STATE >> "+hookOut+"\"\n")
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v: %s", session, err, out)
	}
	stream := &hostStream{}
	term := attachIn(t, base, session, startOpts{out: stream})
	renameWindow(t, term, "AGENT")
	dir := artifactDir(t)

	if got := sessionHostFocus(t, base, session); got != "unknown" {
		t.Fatalf("host_focus is %q before the terminal said anything, want unknown", got)
	}

	setState := func(state string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-agent-state", state, "-s", session, "-w", "AGENT"); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", state, err, out)
		}
	}
	hookLines := func() string {
		b, _ := os.ReadFile(hookOut)
		return string(b)
	}

	// Focused: the pane the person is looking at raises nothing.
	if err := term.Type("\x1b[I"); err != nil {
		t.Fatal(err)
	}
	waitHostFocus(t, base, session, "focused")
	info, _ := tuiosCLI(t, base, "session-info", "-s", session)
	if !strings.Contains(info, "host focus") || !strings.Contains(info, "focused") {
		t.Errorf("session-info does not show the host focus:\n%s", info)
	}
	mark := len(stream.bytes())
	setState("needs_input")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), needsInputGlyph)
	}, uiTimeout); err != nil {
		t.Fatalf("the state never reached the client: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(2 * time.Second)
	if got := notifySince(stream, mark); len(got) > 0 {
		t.Errorf("a notification went to a focused terminal for the pane it shows: %q", got)
	}
	if got := hookLines(); got != "" {
		t.Errorf("the after-agent-state hook fired for the pane a focused terminal shows: %q", got)
	}
	saveArtifact(t, term, dir, "focused-held")
	_ = os.WriteFile(filepath.Join(dir, "focused-held.ansi"), stream.bytes()[mark:], 0o644)
	setState("working")

	// Out of focus: the same transition on the same pane is announced.
	if err := term.Type("\x1b[O"); err != nil {
		t.Fatal(err)
	}
	waitHostFocus(t, base, session, "unfocused")
	mark = len(stream.bytes())
	setState("needs_input")
	deadline := time.Now().Add(uiTimeout)
	for len(notifySince(stream, mark)) == 0 || !strings.Contains(hookLines(), "needs_input") {
		if time.Now().After(deadline) {
			t.Fatalf("with the terminal out of focus the transition raised no alert: notifications %q, hook %q\n%s",
				notifySince(stream, mark), hookLines(), term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	got := notifySince(stream, mark)
	if !strings.Contains(got[0], "AGENT") {
		t.Errorf("the notification does not name the pane: %q", got[0])
	}
	_ = os.WriteFile(filepath.Join(dir, "unfocused-alert.ansi"), stream.bytes()[mark:], 0o644)
	_ = os.WriteFile(filepath.Join(dir, "hook.log"), []byte(hookLines()), 0o644)
	t.Logf("notifications after the blur: %q; artifacts in %s", got, dir)

	// A run alerts once for each state, and a client that attaches starts
	// from the states it finds. The turn ends with one alert. A new client
	// alerts for the question, and the same turn's done reported again
	// raises nothing. The after-agent-state hook is the daemon's, which sees
	// every transition, so it is not counted here.
	setState("working")
	mark = len(stream.bytes())
	setState("done")
	waitNotified(t, term, stream, mark, "done")
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	waitExit(t, term, "after the detach")
	again := &hostStream{}
	term = attachIn(t, base, session, startOpts{out: again})
	if err := term.Type("\x1b[O"); err != nil {
		t.Fatal(err)
	}
	waitHostFocus(t, base, session, "unfocused")
	mark = len(again.bytes())
	setState("needs_input")
	waitNotified(t, term, again, mark, "AGENT")
	mark = len(again.bytes())
	setState("done")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), needsInputGlyph)
	}, uiTimeout); err != nil {
		t.Fatalf("done never reached the new client: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(2 * time.Second)
	if got := notifySince(again, mark); len(got) > 0 {
		t.Errorf("ASSERTION: the new client alerted again for a run that had alerted: %q", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "reattach-alerts.ansi"), again.bytes(), 0o644)

	// Focus back: host_focus follows.
	if err := term.Type("\x1b[I"); err != nil {
		t.Fatal(err)
	}
	waitHostFocus(t, base, session, "focused")
}
