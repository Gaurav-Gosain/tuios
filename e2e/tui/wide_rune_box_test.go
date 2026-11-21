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

const (
	wideRune      = "你"
	combiningPair = "é"
	emojiVS16     = "❤️"
)

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

type paneRect struct {
	x0, w      int
	top, bot   int
	leftGlyph  string
	rightGlyph string
}

func writeWideRuneFixture(t *testing.T) string {
	t.Helper()
	body := fmt.Sprintf(wideRuneFixture, wideRune, combiningPair, emojiVS16)
	script := filepath.Join(t.TempDir(), "widefixture.sh")
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return script
}

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
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after %s: %v\n%s", marker, err, term.Snapshot())
	}
}

var paneColsRe = regexp.MustCompile(`PANECOLS=(\d+)`)

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
	rect.top, rect.bot = xRow, xRow
	for rect.top > 0 && s.Cell(x0-1, rect.top-1).Content == rect.leftGlyph {
		rect.top--
	}
	for rect.bot < rows-1 && s.Cell(x0-1, rect.bot+1).Content == rect.leftGlyph {
		rect.bot++
	}
	if blankGlyph(rect.leftGlyph) {
		t.Fatalf("pane has no left border glyph beside its content at column %d", x0-1)
	}
	if blankGlyph(rect.rightGlyph) {
		t.Fatalf("pane has no right border glyph beside its content at column %d, its body is not %d columns wide",
			x0+want, want)
	}
	return rect
}

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

	for r := rect.top; r <= rect.bot; r++ {
		if got := s.Cell(rect.x0-1, r).Content; got != rect.leftGlyph {
			t.Errorf("row %d: left border is %q, want %q", r, got, rect.leftGlyph)
		}
		if got := s.Cell(rect.x0+rect.w, r).Content; got != rect.rightGlyph {
			t.Errorf("row %d: right border is %q, want %q", r, got, rect.rightGlyph)
		}
	}

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
		for i := filled; i < rect.w; i++ {
			if got := s.Cell(rect.x0+i, row).Content; got != " " && got != "" {
				t.Errorf("%s: column %d of row %d should be blank, got %q", tc.name, i, row, got)
			}
		}
	}
}

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
