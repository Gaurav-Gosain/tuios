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
	// The logger is up once the mode is on. Nothing is reported for it: the
	// pane has focus, which is what a program assumes.
	time.Sleep(time.Second)
	expect("logger started", "")

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

	attachIn(t, base, session, startOpts{cols: 120, rows: 40})
	expect("reattach", "^[[I")
}
