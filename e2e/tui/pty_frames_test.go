package tuie2e

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
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
		{name: "frames without reports", knobs: []string{"pty_frame_enter_kb=64", "pty_frame_acks=off"}, frames: true},
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

// Four panes flood at once, and the client reports each frame it draws. The
// daemon sends a pane its next frame only after that report, or after a wait
// when no report comes.
//
// How this could pass wrongly, written down first:
//   - The floods could be too small to turn any stream to frames. The daemon
//     log must count frames sent.
//   - The client could send no report, and every frame goes on the wait.
//     Some frames must go after a report.
//   - The daemon could ignore the reports and send on every turn. Each frame
//     after the first of a stream must go after a report or on the wait.
//   - A pane could stop on a frame that waits for a report that never comes.
//     Each pane must end with its last line and its end marker.
//
// Negative controls: a client that never reports gets no frame after a
// report. A daemon that ignores the reports sends frames that are neither.
func TestFloodedPanesAreFramedAsTheClientDraws(t *testing.T) {
	knobs := []string{"pty_frame_enter_kb=64"}
	setKnobs(t, knobs...)
	if simOn {
		if prev := session.GetDebugLevel(); prev < session.DebugBasic {
			session.SetDebugLevel(session.DebugBasic)
			t.Cleanup(func() { session.SetDebugLevel(prev) })
		}
	}
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig+"[appearance]\nscrollback_lines = 300000\n")
	killDaemon(t, base)
	const sess = "floodfour"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	for i := 1; i < 4; i++ {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, fmt.Sprintf("P%d", i), "--no-focus"); err != nil {
			t.Fatalf("new-window: %v: %s", err, out)
		}
	}
	opts := copyColorOpts
	opts.args = []string{"attach", sess}
	opts.env = append(append([]string{}, opts.env...), testknob.Env+"="+testknob.Merge(os.Getenv(testknob.Env), knobs...))
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 4 }, bootTimeout); err != nil {
		t.Fatalf("client never showed the four panes: %v\n%s", err, term.Snapshot())
	}
	// Tiled, so every pane is in view and drawn.
	windowManagementMode(t, term)
	enableTiling(t, term)
	before := frameCounts(daemonLogs(t, base))
	// About 1.6 MB a pane, while the client is attached.
	for i := range 4 {
		cmd := fmt.Sprintf("seq -f 'F%d-%%g' 1 160000; echo DONE''%d\n", i, i)
		if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", strconv.Itoa(i), cmd); err != nil {
			t.Fatalf("send-text: %v: %s", err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for i := range 4 {
			if !strings.Contains(text, fmt.Sprintf("F%d-160000", i)) || !strings.Contains(text, fmt.Sprintf("DONE%d", i)) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a pane does not end with its last line and its end marker\n%s", term.Snapshot())
	}
	after := frameCounts(daemonLogs(t, base))
	var got frameCount
	for id, c := range after {
		got.streams += c.streams - before[id].streams
		got.sent += c.sent - before[id].sent
		got.paced += c.paced - before[id].paced
		got.unpaced += c.unpaced - before[id].unpaced
	}
	t.Logf("frames: %d streams, %d sent, %d after a report, %d on the wait", got.streams, got.sent, got.paced, got.unpaced)
	if got.sent == 0 {
		t.Fatalf("ASSERTION: the daemon sent no frames")
	}
	if got.paced == 0 {
		t.Errorf("ASSERTION: no frame went after the client reported the last one drawn")
	}
	if free := got.sent - got.streams - got.paced - got.unpaced; free > 0 {
		t.Errorf("ASSERTION: %d frames went before the client reported the last one and before the wait ended", free)
	}
}

// frameCount is one pane's frame counts from the daemon log.
type frameCount struct{ streams, sent, paced, unpaced int }

var frameCountRE = regexp.MustCompile(`PTY (\S+) frames: (\d+) streams, (\d+) sent, (\d+) after the client drew the last, (\d+) with no report`)

// frameCounts is the last frame counts the daemon log gives for each pane.
func frameCounts(logs string) map[string]frameCount {
	out := map[string]frameCount{}
	for _, m := range frameCountRE.FindAllStringSubmatch(logs, -1) {
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		out[m[1]] = frameCount{streams: n(2), sent: n(3), paced: n(4), unpaced: n(5)}
	}
	return out
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
