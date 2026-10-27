package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// dockRow returns the last non-empty row, which is where the dock lives, so an
// assertion can say "the message is in the dock" rather than "the message is
// somewhere on screen". That distinction is the whole point of this design: the
// old toasts were on screen too, drawn on top of a pane.
func dockRow(s tuitest.Screen) string {
	_, rows := s.Size()
	for y := rows - 1; y >= 0; y-- {
		if line := strings.TrimRight(s.Line(y), " "); line != "" {
			return line
		}
	}
	return ""
}

// paneRows returns everything above the dock, so a test can assert a message is
// absent from the region the old renderer used to cover.
func paneRows(s tuitest.Screen) string {
	_, rows := s.Size()
	var b strings.Builder
	for y := range rows - 2 {
		b.WriteString(s.Line(y))
		b.WriteString("\n")
	}
	return b.String()
}

// TestNotificationLandsInTheDockNotOverAPane is the acceptance test for the
// placement decision. A message must be readable in the dock and must not
// appear anywhere in the pane region, which is what the previous corner toast
// did and what the redesign exists to stop.
//
// It runs in both notifications.style values. full draws the severity cap
// left of the mark and lights the rule above the block in the severity colour
// as a countdown. plain draws neither (#1208): the cap's column is blank and
// the rule above is the dock's own hairline, one colour end to end. The mark
// stays in both, so the kind of message is still readable. A frame of each
// is saved under TUIOS_E2E_FRAMES.
func TestNotificationLandsInTheDockNotOverAPane(t *testing.T) {
	for _, style := range []string{"full", "plain"} {
		t.Run(style, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[notifications]\nstyle = \""+style+"\"\nduration = 30\n")
			term := startIn(t, base, startOpts{cols: 120, rows: 40})
			waitBoot(t, term)
			newWindow(t, term)

			// Toggling tiling pushes a message the app itself generates, so
			// this does not depend on a test-only injection path. Tiling
			// starts off, because startup.tiled ships on and the message this
			// waits for is the one the key pushes going the other way.
			disableTiling(t, term)
			if err := term.SendKeys("t"); err != nil {
				t.Fatalf("send 't': %v", err)
			}
			if err := term.WaitForText("Tiling on", uiTimeout); err != nil {
				t.Fatalf("no message after toggling tiling: %v\n%s", err, term.Snapshot())
			}
			// The cap and the rule are drawn with the block; one more frame
			// lets a late compose land before they are read.
			time.Sleep(300 * time.Millisecond)

			s := term.Screen()
			if !strings.Contains(dockRow(s), "Tiling on") {
				t.Errorf("message is not in the dock row.\ndock row: %q\n%s", dockRow(s), term.Snapshot())
			}
			if strings.Contains(paneRows(s), "Tiling on") {
				t.Errorf("message appears in the pane region, which is what this design removes\n%s", term.Snapshot())
			}

			dir := artifactDir(t)
			saveArtifact(t, term, dir, "dock-message-"+style)
			savePNG(t, s, shot.XTermPalette(), dir, "dock-message-"+style)

			// The block is cap, space, mark, two spaces, text.
			row, _ := findText(t, term, "Tiling on")
			col := cellColumn(s, row, "Tiling on")
			if row < 1 || col < 5 {
				t.Fatalf("message not found at a readable place (%d,%d)\n%s", col, row, term.Snapshot())
			}
			capCell := s.Cell(col-5, row).Content
			if mark := s.Cell(col-3, row).Content; strings.TrimSpace(mark) == "" {
				t.Errorf("%s: no severity mark left of the text\n%s", style, term.Snapshot())
			}
			// The rule above the block: its cells over the block's span.
			colours := map[tuitest.Color]bool{}
			for x := col - 5; x < col+len("Tiling on"); x++ {
				colours[s.Cell(x, row-1).Fg] = true
			}
			switch style {
			case "full":
				if strings.TrimSpace(capCell) == "" {
					t.Errorf("full: no severity cap left of the mark\n%s", term.Snapshot())
				}
				if !ruleHasTwoInks(s, row-1, colours) {
					t.Errorf("full: the rule above the message is not lit\n%s", term.SnapshotStyled())
				}
			case "plain":
				if strings.TrimSpace(capCell) != "" {
					t.Errorf("plain: the cap %q is still drawn left of the mark\n%s", capCell, term.Snapshot())
				}
				if ruleHasTwoInks(s, row-1, colours) {
					t.Errorf("plain: the rule above the message is lit as a countdown\n%s", term.SnapshotStyled())
				}
			}
		})
	}
}

// cellColumn is the cell column where text starts on row, or -1. It counts
// cells, not runes: a Nerd Font mark can take two cells and one rune.
func cellColumn(s tuitest.Screen, row int, text string) int {
	cols, _ := s.Size()
	for x := range cols {
		var b strings.Builder
		for i := x; i < cols && b.Len() < len(text); i++ {
			b.WriteString(s.Cell(i, row).Content)
		}
		if strings.HasPrefix(b.String(), text) {
			return x
		}
	}
	return -1
}

// ruleHasTwoInks reports whether the rule row is drawn in more than one
// colour: span holds the colours of its cells over the message, and the rest
// of the row is the dock's own hairline.
func ruleHasTwoInks(s tuitest.Screen, row int, span map[tuitest.Color]bool) bool {
	all := map[tuitest.Color]bool{}
	cols, _ := s.Size()
	for x := range cols {
		if strings.TrimSpace(s.Cell(x, row).Content) != "" {
			all[s.Cell(x, row).Fg] = true
		}
	}
	for c := range span {
		all[c] = true
	}
	return len(all) > 1
}

// TestNotificationDismissesOnEscAndTheKeyStillLands pins the two halves of the
// dismissal contract. Esc must clear the message, and it must NOT be swallowed:
// a user pressing esc meant it for whatever they were doing, and a notification
// happening to be on screen must not cost them the keypress.
func TestNotificationDismissesOnEscAndTheKeyStillLands(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	disableTiling(t, term)
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send 't': %v", err)
	}
	if err := term.WaitForText("Tiling on", uiTimeout); err != nil {
		t.Fatalf("no message to dismiss: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Tiling on")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not dismiss the message: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after dismissing a notification with esc")
}

// TestQueuedMessagesShowACounter proves the overflow affordance exists. With
// several messages pushed in quick succession the newest is shown and the rest
// are counted, so nothing is silently lost the way the old stack lost the
// fourth toast.
func TestQueuedMessagesShowACounter(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	for range 3 {
		if err := term.SendKeys("t"); err != nil {
			t.Fatalf("send 't': %v", err)
		}
		time.Sleep(120 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "+")
	}, uiTimeout); err != nil {
		t.Logf("no overflow counter seen; dock row: %q", dockRow(term.Screen()))
		t.Logf("%s", term.Snapshot())
		t.Fatalf("queued messages did not produce a counter: %v", err)
	}
}

// TestDockStaysWithinTheScreen guards the width contract at a narrow size,
// where the message block competes with the dock's own items. A block that
// overruns here is how a dock ends up wrapping onto a second row and eating a
// row of terminal.
func TestDockStaysWithinTheScreen(t *testing.T) {
	for _, cols := range []int{80, 100, 120} {
		t.Run(fmt.Sprintf("%dcols", cols), func(t *testing.T) {
			term, _ := start(t, startOpts{cols: cols, rows: 30})
			waitBoot(t, term)
			newWindow(t, term)
			disableTiling(t, term)
			if err := term.SendKeys("t"); err != nil {
				t.Fatalf("send 't': %v", err)
			}
			if err := term.WaitForText("Tiling on", uiTimeout); err != nil {
				t.Fatalf("no message at %d cols: %v\n%s", cols, err, term.Snapshot())
			}
			s := term.Screen()
			w, _ := s.Size()
			row := dockRow(s)
			if got := len([]rune(row)); got > w {
				t.Errorf("dock row is %d cells wide at %d cols:\n%q\n%s", got, w, row, term.Snapshot())
			}
			for _, r := range row {
				if r == '\t' || r == '\r' || r == '\v' {
					t.Errorf("dock row carries a control character at %d cols: %q", cols, row)
					break
				}
			}
		})
	}
}
