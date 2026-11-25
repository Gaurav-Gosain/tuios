package tuie2e

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"github.com/Gaurav-Gosain/tuitest"
)

// A pane that floods is sent to the client as frames of its screen, not as
// bytes, and the lines that scrolled off meanwhile are read from the daemon.
//
// How this could pass wrongly, written down first:
//   - The flood could be too small to turn the stream to frames, and the
//     test checks the byte path. The daemon log must say a stream was sent
//     as frames, and the fallback must not say it.
//   - The last frame could be the last thing drawn, and the bytes after it
//     never come. The screen must end with the last lines, the end marker and
//     the prompt under them.
//   - A frame could leave the client's history numbered wrongly, and copy
//     mode shows blank rows or the wrong lines. The rows above the screen must show lines
//     from the end of the flood, in order, and the top must be HIST1 to
//     HIST5, which only the daemon holds after a frame.
//   - With the feature off the flood could stall. The fallback must reach the
//     same end.
//
// Negative controls: with the client keeping its history through a frame,
// the rows above the screen show lines from early in the flood. With the window taking an
// empty history for a clear on both sides, the top of the history is blank.
func TestFloodedPaneIsDrawnFromFrames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		knobs  []string
		frames bool
	}{
		{name: "frames", knobs: []string{"pty_frame_enter_kb=64"}, frames: true},
		{name: "fallback", knobs: []string{"pty_frame_enter_kb=64", "pty_frames=off"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setKnobs(t, tc.knobs...)
			// The line the test reads is at the basic log level, which the
			// real daemon logs at and the in-process one does not.
			if simOn {
				if prev := session.GetDebugLevel(); prev < session.DebugBasic {
					session.SetDebugLevel(session.DebugBasic)
					t.Cleanup(func() { session.SetDebugLevel(prev) })
				}
			}
			base := t.TempDir()
			writeConfig(t, base, copyCursorConfig+"[appearance]\nscrollback_lines = 300000\n")
			killDaemon(t, base)
			const sess = "floodframes"
			if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
				t.Fatalf("create the session: %v: %s", err, out)
			}
			opts := copyColorOpts
			opts.args = []string{"attach", sess}
			// Under sim the daemon shares the client's knobs, which come in
			// the client's environment.
			opts.env = append(append([]string{}, opts.env...), testknob.Env+"="+testknob.Merge(os.Getenv(testknob.Env), tc.knobs...))
			term := startIn(t, base, opts)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			// Under sim the daemon log is the process's, which holds the
			// lines of the tests before this one too.
			framedBefore := strings.Count(daemonLogs(t, base), "sent as frames")
			// About 2.2 MB, while the client is attached.
			if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "0", "seq -f 'HIST%g' 1 200000; echo FLOOD''END\n"); err != nil {
				t.Fatalf("send-text: %v: %s", err, out)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				text := s.Text()
				return hasHistRun(text, 199998, 200000) && strings.Contains(text, "FLOODEND")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the pane does not end with the last lines and the end marker\n%s", term.Snapshot())
			}
			framed := strings.Count(daemonLogs(t, base), "sent as frames") > framedBefore
			if framed != tc.frames {
				t.Fatalf("ASSERTION: the daemon log says a stream was sent as frames: %v, want %v", framed, tc.frames)
			}

			enterCopyModeHere(t, term, "copy mode on the flooded pane")
			// Forty rows up shows the lines just above the screen, in order.
			// A client that numbered its own old lines as the newest ones
			// shows lines from early in the flood here.
			ups := make([]any, 40)
			for i := range ups {
				ups[i] = "k"
			}
			sendKeys(t, term, ups...)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				text := s.Text()
				// HIST199986 and later are on the screen itself.
				for from := 199800; from <= 199980; from++ {
					if hasHistRun(text, from, from+4) {
						return true
					}
				}
				return false
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: forty rows up does not show the lines just above the screen\n%s", term.Snapshot())
			}
			sendKeys(t, term, "g", "g")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return hasHistRun(s.Text(), 1, 5)
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the top of the history is not HIST1 to HIST5\n%s", term.Snapshot())
			}
		})
	}
}

// daemonLogs is every line the daemon of base logged.
func daemonLogs(t *testing.T, base string) string {
	t.Helper()
	logs, err := tuiosCLI(t, base, "logs", "--all")
	if err != nil {
		t.Fatalf("tuios logs: %v: %s", err, logs)
	}
	return logs
}
