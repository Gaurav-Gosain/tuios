package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const yankFlashConfig = "cursor_bg = \"#ff00ff\"\nbg = \"#101018\"\n" + `
[[keybindings.copy_pipe]]
key = "p"
command = "cat"
`

// TestCopyFlashSweepsACopyModeYank selects a line in copy mode, yanks it, and
// watches the sweep the yank starts.
func TestCopyFlashSweepsACopyModeYank(t *testing.T) {
	for _, run := range []struct {
		name   string
		keys   []string
		yank   string
		daemon bool
	}{
		{"visual-y", []string{"v", "$"}, "y", false},
		{"line-y-daemon", []string{"V"}, "y", true},
		{"copy-pipe", []string{"V"}, "p", false},
	} {
		t.Run(run.name, func(t *testing.T) {
			term := startFlashClientWith(t, "full", run.daemon, yankFlashConfig)
			const marker = "YANK-alpha-bravo-charlie-delta-echo-foxtrot-golf-hotel"
			runInShell(t, term, "clear; echo "+splitMarker(marker), marker, shellTimeout)
			row, col := echoedRow(t, term, marker)
			end := col + len(marker) - 1
			time.Sleep(300 * time.Millisecond)

			if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
				t.Fatalf("send prefix+[: %v", err)
			}
			waitCopyCursor(t, term, "prefix+[")
			if err := term.SendKeys("?YANK-alpha", tuitest.Enter); err != nil {
				t.Fatalf("search: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r, c, ok := copyCursorCell(s)
				return ok && r == row && c == col
			}, uiTimeout); err != nil {
				t.Fatalf("the search did not land on the marker at row %d col %d\n%s", row, col, term.Snapshot())
			}
			for _, k := range run.keys {
				if err := term.SendKeys(k); err != nil {
					t.Fatalf("send %s: %v", k, err)
				}
				time.Sleep(150 * time.Millisecond)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return s.Cell(col+5, row).Bg.Kind != tuitest.ColorDefault
			}, uiTimeout); err != nil {
				t.Fatalf("no selection on the marker\n%s", term.Snapshot())
			}

			frames := sampleFlash(t, term, row, row, row, func() {
				if err := term.SendKeys(run.yank); err != nil {
					t.Fatalf("send %s: %v", run.yank, err)
				}
			})
			logFlash(t, frames)
			saveMultiCopyFrame(t, term, "after")
			checkSmoothSweep(t, frames, col, end)
		})
	}
}

// TestCopyFlashSweepsAMultiCopyYank yanks one line from each of two panes in
// multi copy mode, and requires the sweep to cross both lines and not the pane
// without a selection.
func TestCopyFlashSweepsAMultiCopyYank(t *testing.T) {
	term := startFlashClientWith(t, "full", false, yankFlashConfig+"\n[startup]\ntiled = true\n")
	windowManagementMode(t, term)
	setUpMultiCopyPanes(t, term, true)

	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	if err := term.WaitForText("MULTI 3", uiTimeout); err != nil {
		t.Fatalf("no multi copy mode: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	type span struct{ row, lo, hi int }
	var spans []span
	for _, line := range multiCopyLines {
		r, c := outputCell(t, s, line)
		spans = append(spans, span{r, c, c + len(line) - 1})
	}
	if err := term.SendKeys("/lldp", tuitest.Enter); err != nil {
		t.Fatalf("type search: %v", err)
	}
	if err := term.WaitForText("found in 2 of 3 panes", uiTimeout); err != nil {
		t.Fatalf("the search did not report 2 of 3 panes: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("V"); err != nil {
		t.Fatalf("send V: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(spans[0].lo+5, spans[0].row).Bg.Kind != tuitest.ColorDefault &&
			s.Cell(spans[1].lo+5, spans[1].row).Bg.Kind != tuitest.ColorDefault
	}, uiTimeout); err != nil {
		t.Fatalf("V did not select the match in both matched panes\n%s", term.Snapshot())
	}
	r0, r1 := spans[0].row, spans[0].row
	for _, sp := range spans {
		r0, r1 = min(r0, sp.row), max(r1, sp.row)
	}

	frames := sampleFlash(t, term, r0, r1, spans[0].row, func() {
		if err := term.SendKeys("y"); err != nil {
			t.Fatalf("send y: %v", err)
		}
	})
	logFlash(t, frames)
	saveMultiCopyFrame(t, term, "after")

	litIn := func(f flashFrame, sp span) bool {
		row := f.mask[sp.row-r0]
		for c := sp.lo; c <= sp.hi && c < len(row); c++ {
			if row[c] {
				return true
			}
		}
		return false
	}
	for i, sp := range spans {
		n := 0
		for _, f := range frames {
			if litIn(f, sp) {
				n++
			}
		}
		switch {
		case i < 2 && n < 20:
			t.Errorf("the sweep reached %q in %d frames; an animation needs at least 20", multiCopyLines[i], n)
		case i == 2 && n > 0:
			t.Errorf("the sweep lit %q, which had no selection, in %d frames", multiCopyLines[i], n)
		}
	}
}
