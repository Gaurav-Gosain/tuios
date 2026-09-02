package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The report this file exists for: scroll the strip in the scrolling (niri)
// layout, switch workspace, come back, and the scroll is gone.
//
// One real tuios on one real daemon, three named panes, the real leader chord
// for the workspace switch and real SGR wheel reports for the scroll. The
// assertion is the frame: where the columns are drawn is where the strip is.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named row fail:
//
//   - ScrollingOnFocusChange calling ScrollToFocusedColumn again, which is the
//     shipped code before the fix: TestWorkspaceRoundTripKeepsTheScrolledStrip
//     fails with ALPHA drawn back at the left edge after the round trip.
//   - EnsureFocusedVisible never revealing anything:
//     TestWorkspaceRoundTripRevealsAHiddenColumn fails with the focused column
//     still off screen. That is the positive control on the row above: a fix
//     that only stopped the strip moving would leave the user typing into a
//     pane they cannot see, and this suite would say so.
//
// The strip's own rule and its limit are pinned faster in
// internal/app.TestWorkspaceRoundTripKeepsTheParkedStrip, whose controls
// include the one that matters most here: giving EnsureFocusedVisible the peek
// margin, so it moves an already-visible column. That fails the "kept" row too,
// which is how these tests are known to be bound to the threshold rather than
// to the name of the function called.

// stripClient brings up one client on one daemon session with three named
// panes in the scrolling layout, focused on the leftmost column.
func stripClient(t *testing.T, base string) *tuitest.Terminal {
	t.Helper()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "wsstrip", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	a := attachIn(t, base, "wsstrip", startOpts{cols: 100, rows: 30})
	renameWindow(t, a, "ALPHA")
	newWindow(t, a)
	renameWindow(t, a, "BRAVO")
	newWindow(t, a)
	renameWindow(t, a, "CHARLIE")
	waitWindowCount(t, a, 3, "three panes")
	enableTiling(t, a)

	if err := a.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	waitPaletteOpen(t, a, "to pick the scrolling layout")
	if err := a.SendKeys("scrolling", tuitest.Enter); err != nil {
		t.Fatalf("pick the scrolling layout: %v", err)
	}
	waitPaletteClosed(t, a, "after picking the scrolling layout")

	// Columns are 55% of the screen each, so at most two are ever on screen.
	// Step left to the first column, which is where every row below starts.
	for range 2 {
		if err := a.SendKeys(tuitest.Alt(tuitest.Left)); err != nil {
			t.Fatalf("step left: %v", err)
		}
		time.Sleep(insertGuard)
	}
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ALPHA") && !strings.Contains(s.Text(), "CHARLIE")
	}, uiTimeout); err != nil {
		t.Fatalf("the strip never settled on the first column: %v\n%s", err, a.Snapshot())
	}
	if err := a.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled: %v", err)
	}
	return a
}

// stripBand is the screen without the dock's last two rows, so a notification
// expiring does not read as the strip having moved.
func stripBand(term *tuitest.Terminal) string {
	s := term.Screen()
	_, rows := s.Size()
	var b strings.Builder
	for r := range rows - 2 {
		b.WriteString(s.Line(r))
		b.WriteString("\n")
	}
	return b.String()
}

// wheelStrip turns the wheel with alt held, which is how the strip is scrolled.
func wheelStrip(t *testing.T, term *tuitest.Terminal, notches int) {
	t.Helper()
	for range notches {
		mousePress(t, term, 50, 15, tuitest.MouseWheelDown, tuitest.ModAlt)
	}
	time.Sleep(time.Second)
}

// roundTrip switches to workspace 2 and back to workspace 1.
func roundTrip(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	switchWorkspace(t, term, "2", 0)
	time.Sleep(500 * time.Millisecond)
	switchWorkspace(t, term, "1", 3)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled after the round trip: %v", err)
	}
	time.Sleep(time.Second)
}

// TestWorkspaceRoundTripKeepsTheScrolledStrip is the report. One wheel notch
// moves the strip a fifth of the screen and leaves the focused column on
// screen, so nothing about the round trip has any reason to move it.
func TestWorkspaceRoundTripKeepsTheScrolledStrip(t *testing.T) {
	a := stripClient(t, t.TempDir())

	wheelStrip(t, a, 1)
	before := stripBand(a)
	t.Logf("the strip after one notch:\n%s", before)
	if !strings.Contains(before, "ALPHA") {
		t.Fatalf("one notch took the focused column off screen, so this row proves "+
			"nothing about a column the user can see:\n%s", before)
	}

	roundTrip(t, a)

	if after := stripBand(a); after != before {
		t.Errorf("a workspace round trip moved the strip. The focused column was on "+
			"screen the whole time, so nothing asked for it.\nBEFORE:\n%s\nAFTER:\n%s",
			before, after)
	}
}

// TestWorkspaceRoundTripRevealsAHiddenColumn is the limit on the row above, and
// the positive control for it: with none of the focused column on screen, the
// round trip must bring it back. A fix that simply stopped the strip moving
// would leave the user typing into a pane they cannot see.
func TestWorkspaceRoundTripRevealsAHiddenColumn(t *testing.T) {
	a := stripClient(t, t.TempDir())

	// Five notches is the whole strip: the focused column is off the left end.
	wheelStrip(t, a, 5)
	before := stripBand(a)
	t.Logf("the strip parked off the focused column:\n%s", before)
	if strings.Contains(before, "ALPHA") {
		t.Fatalf("the focused column is still on screen, so this proves nothing:\n%s", before)
	}

	roundTrip(t, a)

	after := stripBand(a)
	if !strings.Contains(after, "ALPHA") {
		t.Errorf("the round trip left the focused column off screen\nAFTER:\n%s", after)
	}
}

// A strip parked so that the focused column is entirely off screen is a place
// the user chose. The rows below are the events that used to take it away, and
// none of them is something the user did.
//
// tileAllWindows revealed the focused column on every retile, and a retile is
// not a focus change: a second client attaching, a routed set-config, a config
// file reload, a peer opening its sidebar or moving its dock, any client adding
// or closing a window on any workspace. The strip snapped to the focused column
// and, because the offset is session state, pushed that snap to every peer. The
// note this answers is the one left open on ScrollingOnFocusChange.
//
// It looked random because the state-sync path repaired its own damage:
// ApplyStateSync retiles in adoptSyncedWindows and then restores the session's
// offset in adoptScrollStrip, so a sync survived and everything else did not.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named row fail:
//
//   - tileAllWindows' scrolling branch revealing again, which is the shipped
//     code before this fix: TestAPeerJoiningLeavesTheParkedStripAlone and
//     TestARemoteConfigChangeLeavesTheParkedStripAlone both fail with ALPHA
//     drawn back on screen, ten times out of ten.
//   - The same mutation leaves TestWorkspaceRoundTripKeepsTheScrolledStrip and
//     TestWorkspaceRoundTripRevealsAHiddenColumn passing, which is how the
//     reveal is known to belong to ScrollingOnFocusChange and not to the
//     retile.
//   - TestAParkedStripSurvivesNothingHappening passes under every mutation
//     above. It is the row that says the two above are measuring an event
//     rather than the passage of time.
//   - The reveal moved out of the retile and into GetOrCreateScrollingLayout,
//     where a strip is built for the first time. Taking it out of there
//     instead fails internal/app.TestJoiningClientLandsOnTheSessionsStrip and
//     TestWorkspaceSwitchLandsBothClientsOnOneStrip, both saying the strip is
//     at home with the focused column off screen. That is the pair that keeps
//     a client from starting up scrolled away from the pane it is typing into.

// parkStripPastFocus scrolls the strip until the focused column is entirely off
// screen, and returns the frame it settled on.
func parkStripPastFocus(t *testing.T, term *tuitest.Terminal) string {
	t.Helper()
	wheelStrip(t, term, 5)
	band := stripBand(term)
	if strings.Contains(band, "ALPHA") {
		t.Fatalf("five notches left the focused column on screen, so nothing below "+
			"proves anything about a parked strip:\n%s", band)
	}
	return band
}

// TestAPeerJoiningLeavesTheParkedStripAlone is the plainest of them: a second
// client attaches and the first client's strip jumps. The joining client tiles
// on startup, snaps its own strip to the focused column, and pushes the offset
// to the session, so the snap reaches the client whose user was reading.
func TestAPeerJoiningLeavesTheParkedStripAlone(t *testing.T) {
	base := t.TempDir()
	a := stripClient(t, base)

	before := parkStripPastFocus(t, a)
	t.Logf("the parked strip:\n%s", before)

	b := attachIn(t, base, "wsstrip", startOpts{cols: 100, rows: 30})
	time.Sleep(3 * time.Second)

	after := stripBand(a)
	if strings.Contains(after, "ALPHA") {
		t.Fatalf("a second client attaching dragged the strip back to the focused column:\n"+
			"BEFORE:\n%s\nAFTER:\n%s", before, after)
	}
	if after != before {
		t.Errorf("a second client attaching moved the strip:\nBEFORE:\n%s\nAFTER:\n%s",
			before, after)
	}

	// The joiner's own screen, because the offset is session state and the
	// screen is the only place this suite can read it. A joiner that snapped
	// its own strip to the focused column would have pushed that offset to the
	// session, and the row above would then be passing on timing alone.
	joined := stripBand(b)
	if strings.Contains(joined, "ALPHA") {
		t.Errorf("the joining client landed on the focused column instead of the session's "+
			"strip, so it pushed its own offset to every peer:\n%s", joined)
	}
}

// TestClosingTheFocusedColumnStillRevealsTheStrip is the positive control on
// the row above. Taking the reveal out of the retile must not take it out of
// the events that really changed which column is focused: closing the focused
// column moves focus to a neighbour, and the strip has to go and show it.
func TestClosingTheFocusedColumnStillRevealsTheStrip(t *testing.T) {
	base := t.TempDir()
	a := stripClient(t, base)

	before := parkStripPastFocus(t, a)
	t.Logf("the parked strip:\n%s", before)

	// ALPHA is the focused column and it is off screen. Closing it hands focus
	// to a column that must be brought on screen.
	if out, err := tuiosCLI(t, base, "send-text", "exit\n", "--window", "ALPHA"); err != nil {
		t.Fatalf("close ALPHA: %v: %s", err, out)
	}
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "ALPHA")
	}, uiTimeout); err != nil {
		t.Fatalf("ALPHA never closed, so this row proves nothing: %v\n%s", err, a.Snapshot())
	}
	time.Sleep(2 * time.Second)

	after := stripBand(a)
	if !strings.Contains(after, "BRAVO") && !strings.Contains(after, "CHARLIE") {
		t.Fatalf("closing the focused column left the strip pointing at nothing on screen:\n%s",
			after)
	}
	alive(t, a, "after closing the focused column")
}

// TestARemoteConfigChangeLeavesTheParkedStripAlone is the same fault reached
// from the other side: a setting changed from the CLI or by an agent is routed
// to every client, and absorbing it retiles.
func TestARemoteConfigChangeLeavesTheParkedStripAlone(t *testing.T) {
	base := t.TempDir()
	a := stripClient(t, base)

	before := parkStripPastFocus(t, a)

	if out, err := tuiosCLI(t, base, "set-config", "border_style", "rounded"); err != nil {
		t.Fatalf("set-config: %v: %s", err, out)
	}
	time.Sleep(3 * time.Second)

	after := stripBand(a)
	if strings.Contains(after, "ALPHA") {
		t.Fatalf("a remote setting change dragged the strip back to the focused column:\n"+
			"BEFORE:\n%s\nAFTER:\n%s", before, after)
	}
}

// TestAParkedStripSurvivesNothingHappening is the control on both rows above. A
// parked strip left alone must stay where it is, so a failure there is an event
// and not the clock.
func TestAParkedStripSurvivesNothingHappening(t *testing.T) {
	a := stripClient(t, t.TempDir())

	before := parkStripPastFocus(t, a)
	time.Sleep(8 * time.Second)

	if after := stripBand(a); after != before {
		t.Errorf("the strip moved with nothing happening at all:\nBEFORE:\n%s\nAFTER:\n%s",
			before, after)
	}
}
