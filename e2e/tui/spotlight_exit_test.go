package tuie2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
)

// The way out of the spotlight, driven against the real binary.
//
// People who switched the beam on by accident saw "Spotlight: ON" for a few
// seconds and then a dimmed screen with nothing on it that said why or how to
// undo it. These hold the fix to what a person sees and presses: a chip in the
// dock while the beam is on, esc in window mode, a click on the chip, and the
// leader chord in terminal mode, where esc stays with the program in the pane.

// spotlightChipText is the chip's name for the beam.
const spotlightChipText = "Spotlight"

// spotlightOnMessage and spotlightOffMessage are the dock messages for the
// two states, in window mode.
const (
	spotlightOnMessage  = "Spotlight is on. Press Esc to turn it off."
	spotlightOffMessage = "Spotlight is off."
)

// spotlightChip finds the chip in the dock row: the first "Spotlight" there,
// with its hint after it. ok is false when the dock carries no chip.
func spotlightChip(s tuitest.Screen) (row, col int, ok bool) {
	_, rows := s.Size()
	for y := rows - 1; y >= 0; y-- {
		line := strings.TrimRight(s.Line(y), " ")
		if line == "" {
			continue
		}
		// The last non-empty row is the dock.
		b := strings.Index(line, spotlightChipText+" ")
		if b < 0 || !strings.Contains(line[b:], "turn off") {
			return y, -1, false
		}
		return y, len([]rune(line[:b])), true
	}
	return -1, -1, false
}

// waitSpotlightChip waits for the chip and returns where it is.
func waitSpotlightChip(t *testing.T, term *tuitest.Terminal) (row, col int) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := spotlightChip(s)
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("the dock shows no Spotlight chip while the beam is on: %v\n%s", err, term.Snapshot())
	}
	row, col, _ = spotlightChip(term.Screen())
	return row, col
}

// waitSpotlightOff waits for the off message and for the chip to go.
func waitSpotlightOff(t *testing.T, term *tuitest.Terminal, how string) {
	t.Helper()
	if err := term.WaitForText(spotlightOffMessage, uiTimeout); err != nil {
		t.Fatalf("%s did not turn the spotlight off: %v\n%s", how, err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := spotlightChip(s)
		return !ok
	}, uiTimeout); err != nil {
		t.Fatalf("the Spotlight chip stayed in the dock after %s: %v\n%s", how, err, term.Snapshot())
	}
}

// saveSpotlightFrame keeps the frame as text, styled text and a PNG when
// TUIOS_E2E_FRAMES names a directory.
func saveSpotlightFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, name)
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		savePNG(t, term.Screen(), shot.XTermPalette(), dir, name)
	}
}

// startWithSpotlight boots tuios with the beam already on, as a person who
// switched it on last time finds it: the choice is saved.
func startWithSpotlight(t *testing.T) *tuitest.Terminal {
	t.Helper()
	base := spotlightConfigFile(t, spotlightOn)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	return term
}

// TestSpotlightKeyShowsTheChipAndTheWayOut presses the key and requires the
// message to say how to turn the beam off, and the chip to stay in the dock
// after the message has gone.
func TestSpotlightKeyShowsTheChipAndTheWayOut(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("B"); err != nil {
		t.Fatalf("press B: %v", err)
	}
	if err := term.WaitForText(spotlightOnMessage, uiTimeout); err != nil {
		t.Fatalf("B did not turn the spotlight on with a message that says how to turn it off: %v\n%s",
			err, term.Snapshot())
	}
	waitSpotlightChip(t, term)
	saveSpotlightFrame(t, term, "spotlight-chip-message")

	// The message burns down. The chip is what is left, and it has to be.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), spotlightOnMessage)
	}, 30*time.Second); err != nil {
		t.Fatalf("the message never left the dock: %v\n%s", err, term.Snapshot())
	}
	row, col := waitSpotlightChip(t, term)
	line := term.Screen().Line(row)
	if !strings.Contains(line, "esc") {
		t.Errorf("the chip in window mode does not name esc: %q", line)
	}
	// The chip is the way out, so the beam must not dim it. Its label is a
	// light ink; dimmed, it carries a quarter of its light.
	if label := term.Screen().Cell(col+1, row); spotlightLight(t, label.Fg) < 400 || label.Faint {
		t.Errorf("the chip label is dimmed (%+v, faint %v); the way out is in the dark",
			label.Fg, label.Faint)
	}
	saveSpotlightFrame(t, term, "spotlight-chip-window-mode")
	t.Logf("chip at row %d col %d: %q", row, col, strings.TrimRight(line, " "))
}

// TestEscTurnsTheSpotlightOffInWindowMode. Esc is bound to enter_window_mode,
// which does nothing in window mode, so it is free to be the way out.
func TestEscTurnsTheSpotlightOffInWindowMode(t *testing.T) {
	term := startWithSpotlight(t)
	waitSpotlightChip(t, term)

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	waitSpotlightOff(t, term, "esc in window mode")
	alive(t, term, "after esc turned the spotlight off")
}

// TestEscClosesAnOverlayBeforeTheSpotlight holds the precedence: an open
// overlay takes esc first, and only the next esc reaches the beam.
func TestEscClosesAnOverlayBeforeTheSpotlight(t *testing.T) {
	term := startWithSpotlight(t)
	waitSpotlightChip(t, term)

	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("the palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the palette: %v\n%s", err, term.Snapshot())
	}
	if _, _, ok := spotlightChip(term.Screen()); !ok {
		t.Fatalf("the esc that closed the palette also turned the spotlight off\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc again: %v", err)
	}
	waitSpotlightOff(t, term, "the second esc")
}

// TestClickingTheSpotlightChipTurnsItOff is the way out for a person who does
// not know any key.
func TestClickingTheSpotlightChipTurnsItOff(t *testing.T) {
	term := startWithSpotlight(t)
	row, col := waitSpotlightChip(t, term)

	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	waitSpotlightOff(t, term, "a click on the chip")
	alive(t, term, "after a click turned the spotlight off")
}

// TestEscReachesTheProgramWhileTheSpotlightIsOn. In terminal mode esc belongs
// to the program in the pane: vim leaves insert mode on it, and Claude Code
// gives a double esc a meaning of its own. Both escs of a double press reach
// cat -v at once, and the beam stays on.
func TestEscReachesTheProgramWhileTheSpotlightIsOn(t *testing.T) {
	term := startWithSpotlight(t)
	enterTerminalMode(t, term)
	runInShell(t, term, `echo "$(echo CAT)READY"; cat -v`, "CATREADY", shellTimeout)
	// The positive half: the beam is on, so a chip that is still there after
	// the escs means they left it on.
	waitSpotlightChip(t, term)

	// Two escs 100 ms apart, inside the 300 ms a double esc would take, then
	// a Z to mark the end. They are sent apart because a host that sends ESC
	// ESC Z as one write has sent esc and alt+z, which is a different press.
	for _, k := range []tuitest.Key{tuitest.Esc, tuitest.Esc} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send esc: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.SendKeys("Z", tuitest.Enter); err != nil {
		t.Fatalf("send Z: %v", err)
	}
	// cat -v prints esc as ^[. Both have to arrive, in order, before the Z.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "^[^[Z")
	}, 2*time.Second); err != nil {
		t.Fatalf("the two escs did not reach cat -v: %v\n%s", err, term.Snapshot())
	}
	if _, _, ok := spotlightChip(term.Screen()); !ok {
		t.Errorf("esc in terminal mode turned the spotlight off; it belongs to the program\n%s",
			term.Snapshot())
	}
}

// TestTheLeaderChordTurnsTheSpotlightOffInTerminalMode is the terminal-mode
// way out. The chip names the chord in that mode, and the chord does not
// reach the pane.
func TestTheLeaderChordTurnsTheSpotlightOffInTerminalMode(t *testing.T) {
	term := startWithSpotlight(t)
	enterTerminalMode(t, term)
	runInShell(t, term, `echo "$(echo CAT)READY"; cat -v`, "CATREADY", shellTimeout)

	row, _ := waitSpotlightChip(t, term)
	if line := term.Screen().Line(row); !strings.Contains(line, "ctrl+b") || !strings.Contains(line, "B") {
		t.Errorf("the chip in terminal mode does not name the leader chord: %q", line)
	}
	saveSpotlightFrame(t, term, "spotlight-chip-terminal-mode")

	if err := term.SendKeys(tuitest.Ctrl('b'), "B"); err != nil {
		t.Fatalf("send the leader chord: %v", err)
	}
	waitSpotlightOff(t, term, "ctrl+b, B in terminal mode")

	// The pane is still the one typing, and the B was not typed into it.
	if err := term.SendKeys("Q", tuitest.Enter); err != nil {
		t.Fatalf("type into cat: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "Q") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("cat -v never echoed Q: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "BQ") {
		t.Errorf("the chord's B reached the pane\n%s", term.Snapshot())
	}
}
