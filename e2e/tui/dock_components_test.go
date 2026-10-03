package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// dockRowText is the dock's content row, found by the workspace readout the bar
// has always carried rather than by computing where the dock is.
func dockRowText(t *testing.T, term *tuitest.Terminal) string {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		if text := s.Line(r); strings.Contains(text, "1:") {
			return text
		}
	}
	t.Fatalf("no dock row carries the workspace readout\n%s", term.Snapshot())
	return ""
}

// TestDockDefaultListsDrawTheSameBar is the promise to everyone with no [dock]
// table: writing the arrangement down as three lists of component names changed
// nothing about the bar. The default config and an explicit default plan must
// produce the same row, cell for cell, on the real binary.
func TestDockDefaultListsDrawTheSameBar(t *testing.T) {
	bare, _ := start(t, startOpts{})
	waitBoot(t, bare)
	newWindow(t, bare)
	waitWindowCount(t, bare, 1, "opening a shell for the bare dock")
	if err := bare.WaitStable(uiTimeout); err != nil {
		t.Fatalf("bare screen never settled: %v\n%s", err, bare.Snapshot())
	}
	bareRow := dockRowText(t, bare)

	// The two configs must differ in the dock lists and in nothing else. The
	// [startup] booleans are the one thing a hand-written file does not inherit
	// from the defaults, and tiled changes the dock's own mode chip, so it is
	// spelled out here to match what the bare run loads.
	base := t.TempDir()
	writeConfig(t, base, `
[startup]
tiled = true

[dock]
left   = ["mode", "workspaces", "trail", "tape"]
center = ["windows"]
right  = ["notifications", "copy-help", "cpu", "ram", "clock", "session-controls"]
`)
	listed := startIn(t, base, startOpts{})
	waitBoot(t, listed)
	newWindow(t, listed)
	waitWindowCount(t, listed, 1, "opening a shell for the listed dock")
	if err := listed.WaitStable(uiTimeout); err != nil {
		t.Fatalf("listed screen never settled: %v\n%s", err, listed.Snapshot())
	}
	listedRow := dockRowText(t, listed)

	if bareRow != listedRow {
		t.Fatalf("the default lists draw a different bar\n bare:   %q\n listed: %q\n%s",
			bareRow, listedRow, listed.Snapshot())
	}
}

// TestDockCustomComponentDraws is the contract on the real binary: a command in
// the config file puts its output on the bar.
func TestDockCustomComponentDraws(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/marker"]

[dock.custom.marker]
command = "echo DOCKCELL"
refresh = "once"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")

	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(dockRowText(t, term), "DOCKCELL") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the custom component never drew its cell\n%s", term.Snapshot())
}

// TestDockBrokenComponentLeavesTheBarAlone pins the failure mode. A component
// whose command exits nonzero is absent, and everything else on the bar is
// exactly where it was: a broken cell must never be a broken bar.
func TestDockBrokenComponentLeavesTheBarAlone(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/broken"]

[dock.custom.broken]
command = "exit 3"
refresh = "once"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v\n%s", err, term.Snapshot())
	}

	row := dockRowText(t, term)
	if !strings.Contains(row, "1:1") {
		t.Fatalf("the workspace readout is gone, so a broken component broke the bar: %q\n%s",
			row, term.Snapshot())
	}
}

// TestDockIdleCostWithComponentsStaysLow is the invariant on the real binary:
// a dock carrying components that do not poll must not wake the program. If
// loading the component machinery armed a timer of its own, this is where it
// shows up, and it is the same budget the plain idle guard defends.
func TestDockIdleCostWithComponentsStaysLow(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left  = ["mode", "workspaces", "trail", "custom/once"]
right = ["custom/pushed", "session-controls"]

[dock.custom.once]
command = "echo ONCE"
refresh = "once"

[dock.custom.pushed]
command = "sleep 300"
refresh = "push"
`)
	var wire byteCounter
	statsPath := filepath.Join(base, "tickstats")
	term := startIn(t, base, startOpts{
		out: &wire,
		env: []string{"TUIOS_STATS_FILE=" + statsPath},
	})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "opening three idle shells")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled before idle: %v\n%s", err, term.Snapshot())
	}

	before := wire.n.Load()
	time.Sleep(10 * time.Second)
	idleBytes := wire.n.Load() - before
	t.Logf("idle wire bytes over 10s with once+push components: %d (budget %d)", idleBytes, idleWireBudget)
	if idleBytes > idleWireBudget {
		t.Fatalf("a dock of once and push components wrote %d bytes over an idle window (budget %d); "+
			"the component engine armed a timer\n%s", idleBytes, idleWireBudget, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	waitExit(t, term, "dock idle test quit")

	ticks, work, render := readTickStats(t, statsPath)
	t.Logf("tick stats with components loaded: ticks=%d work=%d render=%d", ticks, work, render)
	if ticks == 0 {
		t.Fatalf("stats file recorded zero ticks")
	}
	if work >= ticks {
		t.Fatalf("tick work %d did not fall below tick count %d with dock components loaded", work, ticks)
	}
	_ = os.Remove(statsPath)
}

// dockFloodRSSBudget is the most the client may hold, in KiB, while a dock
// component floods its stdout. An idle client sits well under 100 MiB. The
// engine used to read the whole flood before it cut it to 64 KiB, so `yes`
// put gigabytes on the heap inside the three second timeout.
const dockFloodRSSBudget = 300 << 10

// TestDockFloodingComponentStaysBounded runs `yes` as a dock component, again
// every second, and holds the client to a memory budget while it does. The
// client must also keep answering the keyboard, and the cell must show the
// first line, which proves the component ran and its output was read.
//
// The memory is sampled often and the test stops at the first sample over the
// budget, so a build without the cap fails fast instead of eating the machine.
func TestDockFloodingComponentStaysBounded(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/flood"]

[dock.custom.flood]
command = "yes DOCKFLOOD"
refresh = "1s"
`)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")

	pid := term.Pid()
	peak := 0
	sawCell := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		rss := rssOf(t, pid)
		peak = max(peak, rss)
		if rss > dockFloodRSSBudget {
			t.Fatalf("the client holds %d KiB while a dock component floods its stdout (budget %d KiB); "+
				"the engine reads the flood without a cap", rss, dockFloodRSSBudget)
		}
		if !sawCell && screenHas(term.Screen(), "DOCKFLOOD") {
			sawCell = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("client peak RSS over 8s of a flooding dock component: %d KiB (budget %d KiB)", peak, dockFloodRSSBudget)
	if !sawCell {
		t.Fatalf("the flooding component never drew its first line, so its output was never read\n%s", term.Snapshot())
	}

	// The component still runs every second, so this is typed into a flood.
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ALIVE$((40+2))", "ALIVE42", shellTimeout)
}

// TestDockComponentCannotSendUnsafeCharacters puts a bidi override, the
// one-byte CSI (U+009B) and a zero-width space in a component's output. None of
// them may reach the host terminal. The printable text around them, a wide
// character and an emoji must still draw, which is the positive half.
func TestDockComponentCannotSendUnsafeCharacters(t *testing.T) {
	base := t.TempDir()
	// printf octal escapes: \342\200\256 is U+202E, \302\233 is U+009B,
	// \342\200\213 is U+200B. The tail is U+6F22 and U+1F600.
	writeConfig(t, base, `
[dock]
left = ["mode", "workspaces", "trail", "custom/unsafe"]

[dock.custom.unsafe]
command = '''printf 'SAFE\342\200\256RL\302\233OK\342\200\213ZW \346\274\242\360\237\230\200\n' '''
refresh = "once"
`)
	var wire lockedBuffer
	term := startIn(t, base, startOpts{out: &wire})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	waitForAll(t, term, uiTimeout, "the component drawing its text", "SAFERLOKZW", "漢")

	sent := wire.String()
	for _, bad := range []struct{ name, seq string }{
		{"the bidi override U+202E", "‮"},
		{"the one-byte CSI U+009B", "\u009b"},
		{"the zero-width space U+200B", "​"},
	} {
		if strings.Contains(sent, bad.seq) {
			t.Errorf("tuios wrote %s to the host terminal from a dock component", bad.name)
		}
	}
	if !strings.Contains(sent, "\U0001F600") {
		t.Errorf("the emoji in the component's output never reached the host terminal")
	}
}
