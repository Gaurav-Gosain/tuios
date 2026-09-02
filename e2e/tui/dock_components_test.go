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

	base := t.TempDir()
	writeConfig(t, base, `
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
