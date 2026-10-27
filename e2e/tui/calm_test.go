package tuie2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Calm mode (appearance.calm, leader Z): nothing moves or blinks, and only the
// messages that need the person reach the dock. Asked for in discussion #1207.
//
// TestCalmModeQuietsAWorkingSession runs one working agent at motion full, the
// level that shimmers, twice: with calm off and with calm on.
//
//   - Calm off: the working name changes look, and the client keeps writing
//     frames and counting motion clock frames. This is the baseline the calm
//     run is compared with, in the same test, on the same machine.
//   - Calm on: the name holds one look, the motion clock never runs, and the
//     client writes almost nothing while the agent works. The dock says calm.
//   - A pane's own notification (OSC 9) is held: its text never reaches the
//     dock, and the dock says "calm 1".
//   - An agent that needs the person still reaches the dock.
//   - Leader Z turns calm off, and the dock says how many messages came in.
//
// How this could pass wrongly, written down first:
//   - The quiet could come from the agent not working, not from calm. Both
//     runs set the same state the same way, and the calm-off run must show
//     the shimmer, or the test fails before the calm run means anything.
//   - The held message could be missing because it was never sent. The same
//     printf is typed in the calm-off run's pane too, where it must show.
//   - "calm" could match text in a pane. The dock line is the first or last
//     row, and the pane is cleared after the printf.
//   - The byte count could be low because the client stalled. The calm run
//     must still answer the needs_input alert and the leader key afterwards.
func TestCalmModeQuietsAWorkingSession(t *testing.T) {
	results := map[bool]calmResult{}
	for _, calm := range []bool{false, true} {
		name := "calm-off"
		if calm {
			name = "calm-on"
		}
		t.Run(name, func(t *testing.T) {
			results[calm] = calmRun(t, calm)
		})
	}
	off, on := results[false], results[true]
	t.Logf("working agent over %v: calm off wrote %d bytes, %d motion frames, %d context switches; calm on wrote %d bytes, %d motion frames, %d context switches",
		calmWindow, off.wrote, off.motion, off.ctxsw, on.wrote, on.motion, on.ctxsw)
}

// calmWindow is how long each run watches the working agent.
const calmWindow = 3 * time.Second

// calmResult is what one run measured while the agent worked.
type calmResult struct {
	looks  int
	wrote  int64
	motion uint64
	ctxsw  int64
}

// calmDockLines is the dock's text: the first and the last row, since the dock
// sits at one of the two.
func calmDockLines(s tuitest.Screen) string {
	_, rows := s.Size()
	return s.Line(0) + "\n" + s.Line(rows-1)
}

func calmRun(t *testing.T, calm bool) calmResult {
	const name = "calmworker"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, fmt.Sprintf(`[startup]
tiled = true
[appearance]
motion = "full"
calm = %v
[notifications.agent]
suppress_focused = false
settle_seconds = 0
`, calm))
	if out, err := tuiosCLI(t, base, "new", "calm", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "calm", "--name", name); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "calm", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	statsPath := filepath.Join(t.TempDir(), "tickstats")
	var wire byteCounter
	env := append([]string{"TUIOS_STATS_FILE=" + statsPath}, chromeDepths[0].env...)
	term := startIn(t, base, startOpts{args: []string{"attach", "calm"}, shippedLooks: true, env: env, animations: true, out: &wire})
	var row, col int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		var ok bool
		row, col, ok = agentNameCells(s, name)
		return ok
	}, bootTimeout); err != nil {
		t.Fatalf("the working agent never reached the rail: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	host := hostPalette(t, "")
	if err := term.WaitStable(uiTimeout); err != nil && calm {
		t.Fatalf("the calm screen never settled: %v", err)
	}

	// The working window: looks of the name, bytes on the wire, and the
	// client's own frame counters.
	before := sampleIdle(t, term.Pid(), statsPath)
	wroteFrom := wire.n.Load()
	looks, frames := sampleName(term, row, col, len(name), calmWindow)
	wrote := wire.n.Load() - wroteFrom
	after := sampleIdle(t, term.Pid(), statsPath)
	for i, f := range frames {
		savePNG(t, f, host, dir, fmt.Sprintf("working-%d", i))
	}
	saveArtifact(t, term, dir, "working")
	res := calmResult{
		looks:  len(looks),
		wrote:  wrote,
		motion: after.stats["motion"] - before.stats["motion"],
		ctxsw:  after.ctxsw - before.ctxsw,
	}
	t.Logf("calm=%v over %v: %d looks, %d bytes, %d motion frames, %d context switches",
		calm, calmWindow, len(looks), wrote, res.motion, res.ctxsw)

	dock := calmDockLines(term.Screen())
	if !calm {
		if len(looks) < 3 || res.motion == 0 {
			t.Fatalf("with calm off the working name took %d looks and %d motion frames; the baseline shimmer is not running", len(looks), res.motion)
		}
		if strings.Contains(dock, " calm") {
			t.Fatalf("the dock says calm with calm off:\n%s", dock)
		}
	} else {
		if len(looks) != 1 {
			t.Fatalf("with calm on the working name took %d looks\n%s", len(looks), term.SnapshotStyled())
		}
		if res.motion != 0 {
			t.Fatalf("with calm on the motion clock ran %d frames", res.motion)
		}
		if wrote > idleWireBudget {
			t.Fatalf("with calm on the client wrote %d bytes in %v while the agent worked", wrote, calmWindow)
		}
		if !strings.Contains(dock, "calm") {
			t.Fatalf("the dock does not say calm:\n%s", dock)
		}
	}

	// A pane's own message. Held with calm on, shown with it off.
	// Typed by the daemon, so the client's mode does not matter.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "calm", "--raw", `printf '\033]9;CALMPING build finished\007'; clear`); err != nil {
		t.Fatalf("type the notification: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "calm", "Enter"); err != nil {
		t.Fatalf("send enter: %v\n%s", err, out)
	}
	if !calm {
		if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(calmDockLines(s), "CALMPING") }, uiTimeout); err != nil {
			t.Fatalf("with calm off the pane's message never reached the dock: %v\n%s", err, term.Snapshot())
		}
		saveArtifact(t, term, dir, "message")
		savePNG(t, term.Screen(), host, dir, "message")
		return res
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(calmDockLines(s), "calm 1") }, uiTimeout); err != nil {
		t.Fatalf("the dock never counted the held message: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	if d := calmDockLines(term.Screen()); strings.Contains(d, "CALMPING") || strings.Contains(d, "Remote:") {
		t.Fatalf("with calm on the pane's message or a remote command's echo reached the dock\n%s", d)
	}
	saveArtifact(t, term, dir, "held")
	savePNG(t, term.Screen(), host, dir, "held")

	// An agent that needs the person still gets through.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "calm", "needs_input", "--message", "CALMASK pick a database"); err != nil {
		t.Fatalf("set-agent-state needs_input: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(calmDockLines(s), "CALMASK") }, uiTimeout); err != nil {
		t.Fatalf("with calm on an agent that needs you never reached the dock: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "needs-you")
	savePNG(t, term.Screen(), host, dir, "needs-you")

	// Leader Z turns it off and says what came in.
	if err := term.SendKeys(tuitest.Ctrl('b'), "Z"); err != nil {
		t.Fatalf("send leader Z: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(calmDockLines(s), "1 message is in the log")
	}, uiTimeout); err != nil {
		t.Fatalf("turning calm off did not say what came in: %v\n%s", err, term.Snapshot())
	}
	if d := calmDockLines(term.Screen()); strings.Contains(d, "calm 1") {
		t.Fatalf("the dock still says calm after leader Z:\n%s", d)
	}
	saveArtifact(t, term, dir, "off")
	savePNG(t, term.Screen(), host, dir, "off")
	return res
}
