package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The border box skips lipgloss's word wrap for a pane body the renderer says
// is already exactly the pane's rectangle (internal/app, sizeContentBox). The
// unit tests for that measure tuios's own strings, which is the kind of proof
// that has already let a bug through here once: a differential harness agreed
// on grid state while the host drew a coloured block behind every filename.
//
// So this asserts on the host's grid, built by a real emulator from the bytes
// tuios actually wrote to its terminal, and on the cases where a skipped wrap
// goes wrong: a rune two columns wide, a combining mark that adds none, an
// emoji made wide by a presentation selector, and lines that are exactly the
// pane's width so there is no slack anywhere to absorb a mistake.
const (
	// wideRune is two columns wide and three bytes long.
	wideRune = "你"
	// combiningPair is two runes and three bytes in one column.
	combiningPair = "é"
	// emojiVS16 is a heart made two columns wide by a variation selector, which
	// is the case where the byte count, the rune count and the column count are
	// three different numbers.
	emojiVS16 = "❤️"
)

// wideRuneFixture fills the pane with rows that are exactly its width. It asks
// the pane's own PTY how wide it is, so the rows are exact whatever size the
// window manager gave the pane.
const wideRuneFixture = `#!/bin/sh
set -- $(stty size)
W=$2
rep() {
  i=0; out=''
  while [ $i -lt $2 ]; do out="$out$1"; i=$((i+1)); done
  printf '%%s\n' "$out"
}
printf 'PANECOLS=%%s\n' "$W"
rep 'X' "$W"
rep '%s' $((W / 2))
rep '%s' "$W"
rep '%s' $((W / 2))
printf 'WIDEDONE\n'
`

// paneRect is the pane's content rectangle on the host grid: the columns the
// guest's own output occupies, and the rows between the two border rows.
type paneRect struct {
	x0, w      int
	top, bot   int
	leftGlyph  string
	rightGlyph string
}

// writeWideRuneFixture writes the fixture script once per test. Two runs of it
// have to share the path: the pane echoes the command that started it, so a
// per-run temporary directory would put a different string on screen and the
// differential would be comparing its own scaffolding.
func writeWideRuneFixture(t *testing.T) string {
	t.Helper()
	body := fmt.Sprintf(wideRuneFixture, wideRune, combiningPair, emojiVS16)
	script := filepath.Join(t.TempDir(), "widefixture.sh")
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return script
}

// paintWideRunes boots a client, runs the fixture in a pane, and returns the
// settled host screen.
func paintWideRunes(t *testing.T, script string, extraEnv ...string) tuitest.Screen {
	t.Helper()
	term, _ := start(t, startOpts{cols: 160, rows: 45, env: extraEnv})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "sh "+script, "WIDEDONE", shellTimeout)
	waitForPromptAfter(t, term, "WIDEDONE")
	return term.Screen()
}

// waitForPromptAfter blocks until the shell has printed its prompt below the
// given marker.
//
// The fixture's last line is not the pane's last line: the shell draws a prompt
// after it, and whether that prompt has landed when the screen is read is a
// race. It cost this file a flake in the full suite, where the differential
// caught its own scaffolding, a "$" in one run against a blank in the other,
// rather than anything about the wrap. Waiting on the prompt is waiting on the
// state both runs have to be compared in.
func waitForPromptAfter(t *testing.T, term *tuitest.Terminal, marker string) {
	t.Helper()
	settled := func(s tuitest.Screen) bool {
		lines := strings.Split(s.Text(), "\n")
		for i, line := range lines {
			if !strings.Contains(line, marker) {
				continue
			}
			for _, below := range lines[i+1:] {
				if strings.Contains(below, "$") {
					return true
				}
			}
			return false
		}
		return false
	}
	if err := term.WaitFor(settled, uiTimeout); err != nil {
		t.Fatalf("shell never returned a prompt after %s: %v\n%s", marker, err, term.Snapshot())
	}
	// And then for the compositor to stop redrawing, so the two runs are read
	// in the same settled frame rather than one frame apart.
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after %s: %v\n%s", marker, err, term.Snapshot())
	}
}

var paneColsRe = regexp.MustCompile(`PANECOLS=(\d+)`)

// findPane locates the pane's content rectangle from the screen itself: the run
// of X the fixture painted gives its left edge and width, and the cells just
// outside that run give the border glyphs the rest of the rows must match.
func findPane(t *testing.T, s tuitest.Screen) paneRect {
	t.Helper()
	cols, rows := s.Size()

	m := paneColsRe.FindStringSubmatch(s.Text())
	if m == nil {
		t.Fatalf("fixture never reported the pane width\n%s", s.Text())
	}
	want, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unreadable pane width %q: %v", m[1], err)
	}

	xRow := -1
	for r := range rows {
		if strings.Count(s.Line(r), "X") == want {
			xRow = r
			break
		}
	}
	if xRow < 0 {
		t.Fatalf("no row carries the %d columns of X the fixture printed\n%s", want, s.Text())
	}

	x0 := -1
	for c := range cols {
		if s.Cell(c, xRow).Content == "X" {
			x0 = c
			break
		}
	}
	if x0 < 0 || x0 == 0 || x0+want >= cols {
		t.Fatalf("pane content at column %d width %d does not fit a %d column grid", x0, want, cols)
	}

	rect := paneRect{
		x0: x0, w: want,
		leftGlyph:  s.Cell(x0-1, xRow).Content,
		rightGlyph: s.Cell(x0+want, xRow).Content,
	}
	// The border rows are the nearest rows above and below where the vertical
	// edge stops being the glyph it is beside the content.
	rect.top, rect.bot = xRow, xRow
	for rect.top > 0 && s.Cell(x0-1, rect.top-1).Content == rect.leftGlyph {
		rect.top--
	}
	for rect.bot < rows-1 && s.Cell(x0-1, rect.bot+1).Content == rect.leftGlyph {
		rect.bot++
	}
	// Both edges have to be a drawn glyph, not merely consistent with
	// themselves. A pane whose body ran a column long pushes its right border
	// off the column it belongs in and leaves a blank there, and calibrating
	// against that blank would let this whole file agree with a broken screen.
	if blankGlyph(rect.leftGlyph) {
		t.Fatalf("pane has no left border glyph beside its content at column %d", x0-1)
	}
	if blankGlyph(rect.rightGlyph) {
		t.Fatalf("pane has no right border glyph beside its content at column %d, its body is not %d columns wide",
			x0+want, want)
	}
	return rect
}

// blankGlyph reports whether a cell holds nothing a user would see.
func blankGlyph(content string) bool {
	return content == "" || content == " "
}

// TestWideRunesKeepThePaneRectangleOnScreen is the on-screen proof for the
// skipped wrap.
//
// Every assertion is on the host's cells rather than on tuios's strings. A
// width mistake in the body cannot hide from them: one column too many pushes
// the right border out of its column on that row, one too few pulls it in, and
// a body the box re-flowed by accident breaks the rows below it.
func TestWideRunesKeepThePaneRectangleOnScreen(t *testing.T) {
	s := paintWideRunes(t, writeWideRuneFixture(t))
	rect := findPane(t, s)
	t.Logf("pane content columns %d..%d on rows %d..%d, border %q %q",
		rect.x0, rect.x0+rect.w-1, rect.top, rect.bot, rect.leftGlyph, rect.rightGlyph)

	// The frame itself. Both vertical edges have to hold their glyph on every
	// body row, which is what a body one column too wide or too narrow breaks.
	for r := rect.top; r <= rect.bot; r++ {
		if got := s.Cell(rect.x0-1, r).Content; got != rect.leftGlyph {
			t.Errorf("row %d: left border is %q, want %q", r, got, rect.leftGlyph)
		}
		if got := s.Cell(rect.x0+rect.w, r).Content; got != rect.rightGlyph {
			t.Errorf("row %d: right border is %q, want %q", r, got, rect.rightGlyph)
		}
	}

	// The wide rows. A wide rune owns two columns: its cluster sits in the
	// first and the second is its continuation, empty and zero width. A pane
	// that bisected one, or that counted its bytes instead of its columns,
	// fails here rather than merely looking wrong.
	for _, tc := range []struct {
		name    string
		cluster string
		width   int
	}{
		{"wide rune", wideRune, 2},
		{"combining mark", combiningPair, 1},
		{"emoji with a presentation selector", emojiVS16, 2},
	} {
		row := rowOf(t, s, rect, tc.cluster)
		filled := (rect.w / tc.width) * tc.width
		for i := 0; i < filled; i += tc.width {
			cell := s.Cell(rect.x0+i, row)
			if cell.Content != tc.cluster {
				t.Fatalf("%s: column %d of row %d holds %q, want %q",
					tc.name, i, row, cell.Content, tc.cluster)
			}
			if cell.Width != tc.width {
				t.Fatalf("%s: column %d of row %d is %d columns wide, want %d",
					tc.name, i, row, cell.Width, tc.width)
			}
			for k := 1; k < tc.width; k++ {
				cont := s.Cell(rect.x0+i+k, row)
				if cont.Width != 0 || cont.Content != "" {
					t.Fatalf("%s: column %d of row %d should be the continuation of the cluster before it, got %q width %d",
						tc.name, i+k, row, cont.Content, cont.Width)
				}
			}
		}
		// Anything the fixture could not fill exactly is blank pane, not the
		// border creeping inwards.
		for i := filled; i < rect.w; i++ {
			if got := s.Cell(rect.x0+i, row).Content; got != " " && got != "" {
				t.Errorf("%s: column %d of row %d should be blank, got %q", tc.name, i, row, got)
			}
		}
	}
}

// rowOf returns the pane row whose first content column starts the given
// cluster.
func rowOf(t *testing.T, s tuitest.Screen, rect paneRect, cluster string) int {
	t.Helper()
	for r := rect.top; r <= rect.bot; r++ {
		if s.Cell(rect.x0, r).Content == cluster {
			return r
		}
	}
	t.Fatalf("no pane row begins with %q\n%s", cluster, s.Text())
	return -1
}

// TestSkippingTheWrapDrawsTheSameScreenAsWrapping is the differential, run on
// the host's grid rather than on tuios's strings.
//
// TUIOS_NO_PRESHAPED=1 sends every pane body back through lipgloss's wrap, so
// the same fixture is painted twice by the same binary down the two paths. The
// comparison includes each cell's colours, because the failure this is guarding
// against is not only a shifted column: the bug that got through the last
// differential harness was a background that reached the host and nothing else.
func TestSkippingTheWrapDrawsTheSameScreenAsWrapping(t *testing.T) {
	script := writeWideRuneFixture(t)
	fast := paintWideRunes(t, script)
	slow := paintWideRunes(t, script, "TUIOS_NO_PRESHAPED=1")

	fastRect := findPane(t, fast)
	slowRect := findPane(t, slow)
	if fastRect != slowRect {
		t.Fatalf("the two paths did not even draw the pane in the same place: %+v vs %+v", fastRect, slowRect)
	}

	diffs := 0
	for r := fastRect.top - 1; r <= fastRect.bot+1; r++ {
		for c := fastRect.x0 - 1; c <= fastRect.x0+fastRect.w; c++ {
			a, b := fast.Cell(c, r), slow.Cell(c, r)
			if a == b {
				continue
			}
			diffs++
			if diffs <= 8 {
				t.Errorf("cell (%d,%d): wrap skipped %+v, wrap run %+v", c, r, a, b)
			}
		}
	}
	if diffs > 0 {
		t.Fatalf("%d cells of the pane differ between the two paths", diffs)
	}
}
