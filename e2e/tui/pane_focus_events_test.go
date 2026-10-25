package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaneFocusEventsFollowTheFocus checks the DECSET 1004 reports a pane gets
// as the focus moves around it, through a real daemon and client.
//
// The pane runs cat -v in raw mode after turning focus reporting on, so the
// log holds every report as ^[[I or ^[[O. Each step waits for the log to hold
// exactly the reports so far, so a report that is missing, sent twice or sent
// to the wrong pane fails the step that caused it.
//
// The empty workspace and the detach are the paths that do not go through
// FocusWindow: a switch to an empty workspace sets the focus to none
// directly, and the detach must not be taken back by the messages the client
// handles on its way out.
//
// The host terminal's focus counts too: a pane hears CSI O when every host
// terminal showing the session has lost focus, and CSI I when one gets it
// back, once per change.
//
// Negative controls: without reportHostFocus the pane reads nothing at "the
// host terminal lost focus". With handleClientFocus reporting each client's
// change on its own, the pane reads a second CSI I at "the second host
// terminal got focus".
//
// The log is copied to the artifact directory.
func TestPaneFocusEventsFollowTheFocus(t *testing.T) {
	base := t.TempDir()
	const session = "focus-events"
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "focus events setup")
	enableTiling(t, term)
	enterTerminalMode(t, term)

	logPath := filepath.Join(base, "focus.log")
	t.Cleanup(func() {
		if data, err := os.ReadFile(logPath); err == nil {
			_ = os.WriteFile(filepath.Join(artifactDir(t), "focus.log"), data, 0o644)
		}
	})
	if err := term.SendKeys(`stty raw -echo; printf '\033[?1004h'; cat -v > `+logPath, tuitest.Enter); err != nil {
		t.Fatal(err)
	}

	want := ""
	expect := func(step, add string) {
		t.Helper()
		want += add
		deadline := time.Now().Add(uiTimeout)
		var got string
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(logPath)
			got = string(data)
			if got == want {
				return
			}
			if !strings.HasPrefix(want, got) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: the pane read %q, want %q", step, got, want)
	}
	// The logger is up once the mode is on. The pane has focus, so turning
	// the mode on gets one focus-in report, as xterm, kitty and ghostty send.
	expect("logger started", "^[[I")

	send := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatal(err)
		}
	}
	send(tuitest.Alt(tuitest.Left))
	expect("focus moved to the other pane", "^[[O")
	send(tuitest.Alt(tuitest.Right))
	expect("focus came back", "^[[I")
	send(tuitest.Ctrl('b'), "w", "3")
	expect("switch to an empty workspace", "^[[O")
	send(tuitest.Ctrl('b'), "w", "1")
	expect("switch back", "^[[I")
	leaderKey(t, term, "d")
	expect("detach", "^[[O")
	term.Close()
	// The client handles a few messages on its way out. None of them may
	// report the focus again, and the reattach below would hide it if one did.
	time.Sleep(time.Second)
	expect("after the client left", "")

	first := attachIn(t, base, session, startOpts{cols: 120, rows: 40})
	expect("reattach", "^[[I")

	// The host terminal's own focus. A person who moves from the terminal to
	// another application is leaving the pane too, and comes back to it.
	host := func(term *tuitest.Terminal, focused bool) {
		t.Helper()
		report := "\x1b[O"
		if focused {
			report = "\x1b[I"
		}
		if err := term.Type(report); err != nil {
			t.Fatal(err)
		}
	}
	quiet := func(step string) {
		t.Helper()
		time.Sleep(time.Second)
		expect(step, "")
	}
	host(first, false)
	expect("the host terminal lost focus", "^[[O")
	host(first, false)
	quiet("the host terminal lost focus again")
	host(first, true)
	expect("the host terminal got focus back", "^[[I")

	// Two clients show the session. The pane has focus while either host
	// terminal has it, so it hears about the change only when the last one
	// goes and the first one comes back.
	second := attachIn(t, base, session, startOpts{cols: 120, rows: 40})
	// The second client reports focus as it attaches; that report is the
	// reattach step's subject, not this one's.
	time.Sleep(time.Second)
	data, _ := os.ReadFile(logPath)
	want = string(data)
	host(second, true)
	quiet("the second host terminal got focus")
	host(first, false)
	quiet("one of two host terminals lost focus")
	host(second, false)
	expect("both host terminals lost focus", "^[[O")
	host(first, true)
	expect("one host terminal got focus back", "^[[I")
}
