package session

import (
	"regexp"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// In a daemon session it is the daemon's emulator that answers a program's
// OSC 11 and OSC 10 queries. The client says what it paints behind pane
// content in the state it pushes, and the daemon hands that to every pane,
// including one made afterwards.

var reportRGB = regexp.MustCompile(`\x1b\]11;(rgb:[0-9a-fA-F/]+)`)

// bgAnswer asks a daemon-side emulator for its background and returns the
// rgb: value it answered with.
func bgAnswer(t *testing.T, p *PTY) string {
	t.Helper()
	p.terminalMu.Lock()
	_, _ = p.terminal.Write([]byte("\x1b]11;?\x1b\\"))
	p.terminalMu.Unlock()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := p.terminal.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if m := reportRGB.FindStringSubmatch(s); m != nil {
			return m[1]
		}
		t.Fatalf("the reply %q carries no colour", s)
	case <-time.After(2 * time.Second):
		t.Fatal("the emulator did not answer OSC 11")
	}
	return ""
}

// fakePTY is a pane with an emulator and no process, which is all an answer
// needs.
func fakePTY(t *testing.T, sess *Session, id string) *PTY {
	t.Helper()
	p := &PTY{ID: id, terminal: vt.New(20, 4)}
	// Taken out again before the session stops, which would close it as a
	// real pane and it has no process to close.
	t.Cleanup(func() {
		sess.ptysMu.Lock()
		delete(sess.ptys, id)
		sess.ptysMu.Unlock()
		_ = p.terminal.Close()
	})
	sess.ptysMu.Lock()
	sess.ptys[id] = p
	sess.ptysMu.Unlock()
	return p
}

func TestPushedReportColorsReachEveryPane(t *testing.T) {
	sess := newTestSession(t)
	a := fakePTY(t, sess, "report-a")
	before := bgAnswer(t, a)

	sess.applyReportColors("#123456", "#eeddcc")
	if got := bgAnswer(t, a); got != "rgb:1212/3434/5656" {
		t.Errorf("after the push the pane answered %q, want the painted rgb:1212/3434/5656", got)
	}

	// A pane made after the push is told at creation, which is inside
	// createPTY; a pane added straight to the map here stands in for the
	// ones that were already there, so the stored pair is checked directly.
	sess.ptysMu.RLock()
	stored := sess.reportBg
	sess.ptysMu.RUnlock()
	if stored == nil {
		t.Error("the session kept no pair for the panes made after the push")
	}

	// A client that paints nothing sends empty, which gives the emulator its
	// own answer back.
	sess.applyReportColors("", "")
	if got := bgAnswer(t, a); got != before {
		t.Errorf("after an empty push the pane answered %q, want its own %q", got, before)
	}
}

// The pair arrives the way a client sends it: in a state push, through the
// daemon's update-state handler.
func TestStatePushCarriesReportColorsToThePanes(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "report")
	tui := attachSyncingTUI(t, d, sess)
	p := fakePTY(t, sess, "report-push")

	state := sess.GetState()
	state.PaneReportBg, state.PaneReportFg = "#123456", "#eeddcc"
	tui.sync(state)
	if got := bgAnswer(t, p); got != "rgb:1212/3434/5656" {
		t.Errorf("after the push the pane answered %q, want the painted rgb:1212/3434/5656", got)
	}

	// A later push that paints nothing takes it back.
	state = sess.GetState()
	state.PaneReportBg, state.PaneReportFg = "", ""
	tui.sync(state)
	if got := bgAnswer(t, p); got == "rgb:1212/3434/5656" {
		t.Error("a push that paints nothing left the painted answer in place")
	}
}

func TestReportColorsRideTheStateFingerprint(t *testing.T) {
	a := &SessionState{Name: "s"}
	b := &SessionState{Name: "s", PaneReportBg: "#123456"}
	if StateFingerprint(a) == StateFingerprint(b) {
		t.Error("a push that changes the painted ground fingerprints the same as one that does not, so it would never be sent")
	}
}
