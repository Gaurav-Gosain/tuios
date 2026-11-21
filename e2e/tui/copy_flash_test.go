package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const flashTestMs = 1000

type flashFrame struct {
	at           time.Duration
	peak         int
	lit          int
	refLo, refHi int
	art          string
	mask         [][]bool
}

func startFlashClient(t *testing.T, motion string, daemon bool) *tuitest.Terminal {
	t.Helper()
	return startFlashClientWith(t, motion, daemon, "")
}

func startFlashClientWith(t *testing.T, motion string, daemon bool, extra string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, fmt.Sprintf(
		"[appearance]\nmotion = %q\n\n[appearance.selection]\nflash = true\nflash_ms = %d\nflash_style = \"horizontal\"\nflash_color = \"#ffffff\"\n",
		motion, flashTestMs)+extra)
	opts := startOpts{
		animations: true,
		env:        []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
	}
	if !daemon {
		term := startIn(t, base, opts)
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)
		return term
	}
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-flash", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	opts.args = []string{"attach", "e2e-flash"}
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	return term
}

func luminance(c tuitest.Color) int {
	if c.Kind != tuitest.ColorRGB {
		return 0
	}
	return int(c.R)*299 + int(c.G)*587 + int(c.B)*114
}

func sampleFlash(t *testing.T, term *tuitest.Terminal, r0, r1, ref int, release func()) []flashFrame {
	t.Helper()
	cols, _ := term.Screen().Size()
	type raw struct {
		at    time.Duration
		cells [][]tuitest.Color
	}
	var frames []raw
	grounds := func(s tuitest.Screen) [][]tuitest.Color {
		cells := make([][]tuitest.Color, r1-r0+1)
		for r := r0; r <= r1; r++ {
			row := make([]tuitest.Color, cols)
			for c := range cols {
				row[c] = s.Cell(c, r).Bg
			}
			cells[r-r0] = row
		}
		return cells
	}
	same := func(a, b [][]tuitest.Color) bool {
		for i := range a {
			for j := range a[i] {
				if a[i][j] != b[i][j] {
					return false
				}
			}
		}
		return true
	}
	release()
	start := time.Now()
	lastChange := start
	for time.Since(start) < 3*flashTestMs*time.Millisecond {
		cells := grounds(term.Screen())
		if len(frames) == 0 || !same(cells, frames[len(frames)-1].cells) {
			frames = append(frames, raw{at: time.Since(start), cells: cells})
			lastChange = time.Now()
		}
		if time.Since(start) > flashTestMs*time.Millisecond && time.Since(lastChange) > 400*time.Millisecond {
			break
		}
		time.Sleep(time.Millisecond)
	}
	settled := 0
	for _, row := range frames[len(frames)-1].cells {
		for _, bg := range row {
			settled = max(settled, luminance(bg))
		}
	}
	out := make([]flashFrame, 0, len(frames))
	for _, f := range frames {
		ff := flashFrame{at: f.at, peak: -1, refLo: -1, refHi: -1}
		best := settled
		var art strings.Builder
		ff.mask = make([][]bool, len(f.cells))
		for ri, row := range f.cells {
			ff.mask[ri] = make([]bool, len(row))
			for c, bg := range row {
				l := luminance(bg)
				lit := l > settled
				if lit {
					ff.lit++
					ff.mask[ri][c] = true
				}
				if r0+ri != ref {
					continue
				}
				switch {
				case lit:
					art.WriteByte('#')
				case bg.Kind != tuitest.ColorDefault:
					art.WriteByte('.')
				default:
					art.WriteByte(' ')
				}
				if lit {
					if ff.refLo < 0 {
						ff.refLo = c
					}
					ff.refHi = c
				}
				if lit && l > best {
					best, ff.peak = l, c
				}
			}
		}
		ff.art = strings.TrimRight(art.String(), " ")
		out = append(out, ff)
	}
	return out
}

func logFlash(t *testing.T, frames []flashFrame) {
	t.Helper()
	for i, f := range frames {
		if i >= 90 {
			t.Logf("... %d more frames", len(frames)-i)
			return
		}
		art := f.art
		if len(art) > 110 {
			art = art[:110]
		}
		t.Logf("%4dms lit=%4d peak=%3d |%s", f.at.Milliseconds(), f.lit, f.peak, art)
	}
}

func checkSmoothSweep(t *testing.T, frames []flashFrame, textStart, textEnd int) {
	t.Helper()
	first, last := -1, -1
	for i, f := range frames {
		if f.lit > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		t.Fatalf("no frame showed the sweep")
	}
	lit := frames[first : last+1]
	if len(lit) < 20 {
		t.Errorf("the sweep reached the screen as %d frames over %dms; an animation needs at least 20",
			len(lit), flashTestMs)
	}
	var worst time.Duration
	for i := 1; i < len(lit); i++ {
		worst = max(worst, lit[i].at-lit[i-1].at)
	}
	if worst > 120*time.Millisecond {
		t.Errorf("the sweep stalled for %v between two frames", worst)
	}
	var onFrom, onTo time.Duration = -1, -1
	for _, f := range lit {
		if f.refLo < 0 {
			continue
		}
		if f.refLo < textStart || f.refHi > textEnd {
			t.Errorf("at %v the light covered columns %d to %d; the text runs from %d to %d",
				f.at, f.refLo, f.refHi, textStart, textEnd)
			break
		}
		if onFrom < 0 {
			onFrom = f.at
		}
		onTo = f.at
	}
	if on := onTo - onFrom; onFrom < 0 || on < flashTestMs*time.Millisecond/2 {
		t.Errorf("the light was on the text for %v of a %dms sweep", max(on, 0), flashTestMs)
	}
	prev, lo, hi := -1, -1, -1
	for _, f := range lit {
		if f.peak < 0 {
			continue
		}
		if f.peak < prev {
			t.Errorf("the light moved back from column %d to %d at %v", prev, f.peak, f.at)
		}
		if lo < 0 {
			lo = f.peak
		}
		prev, hi = f.peak, f.peak
	}
	if lo < 0 || lo > textStart+(textEnd-textStart)/4 || hi < textEnd-(textEnd-textStart)/4 {
		t.Errorf("the light crossed columns %d to %d; the text runs from %d to %d", lo, hi, textStart, textEnd)
	}
}

func echoedRow(t *testing.T, term *tuitest.Terminal, text string) (row, col int) {
	t.Helper()
	findText(t, term, text)
	s := term.Screen()
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		line := s.Line(r)
		before, _, ok := strings.Cut(line, text)
		if !ok {
			continue
		}
		if strings.TrimLeft(before, "│ ") == "" {
			return r, len([]rune(before))
		}
	}
	t.Fatalf("no row starts with %q\n%s", text, term.Snapshot())
	return 0, 0
}

// TestCopyFlashSweepsAShortSelectionSmoothly drags across one line and
// watches the sweep that the copy on release starts. With motion none there
// is no sweep at all.
func TestCopyFlashSweepsAShortSelectionSmoothly(t *testing.T) {
	for _, run := range []struct {
		name   string
		motion string
		daemon bool
	}{
		{"standalone-full", "full", false},
		{"daemon-basic", "basic", true},
		{"standalone-none", "none", false},
	} {
		t.Run(run.name, func(t *testing.T) {
			term := startFlashClient(t, run.motion, run.daemon)
			const marker = "SWEEP-alpha-bravo-charlie-delta-echo-foxtrot-golf-hotel"
			runInShell(t, term, "clear; echo "+splitMarker(marker), marker, shellTimeout)
			row, col := echoedRow(t, term, marker)
			end := col + len(marker) - 1

			mousePress(t, term, col, row, tuitest.MouseLeft, 0)
			for c := col + 1; c <= end; c++ {
				mouseMotion(t, term, c, row, tuitest.MouseLeft, 0)
			}
			frames := sampleFlash(t, term, row, row, row, func() {
				mouseRelease(t, term, end, row, tuitest.MouseLeft, 0)
			})
			logFlash(t, frames)
			if run.motion == "none" {
				for _, f := range frames {
					if f.lit > 0 {
						t.Fatalf("with motion none the copy drew a sweep at %v", f.at)
					}
				}
				return
			}
			checkSmoothSweep(t, frames, col, end)
		})
	}
}

var wallLine = regexp.MustCompile(`WALL-(\d+)-END`)

func oldestWall(s tuitest.Screen) int {
	oldest := -1
	for _, m := range wallLine.FindAllStringSubmatch(s.Text(), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && (oldest < 0 || n < oldest) {
			oldest = n
		}
	}
	return oldest
}

// TestCopyFlashSweepsASelectionIntoScrollback drags from the bottom of a pane
// full of history up past its top edge, so the view scrolls back while the
// selection grows, and watches the sweep that the copy on release starts.
//
// The lines are much shorter than the pane. The sweep used to be sized to
// whole rows, so it spent most of its run crossing the empty right side of
// the pane and was on the text for a fifth of it.
func TestCopyFlashSweepsASelectionIntoScrollback(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			term := startFlashClient(t, "full", daemon)
			fillScrollback(t, term, "WALL", 200)
			s := term.Screen()
			_, rows := s.Size()
			top, bottom := -1, -1
			for r := range rows {
				if wallLine.MatchString(s.Line(r)) {
					if top < 0 {
						top = r
					}
					bottom = r
				}
			}
			if top < 1 || bottom-top < 10 {
				t.Fatalf("the pane shows too little history\n%s", term.Snapshot())
			}
			before := oldestWall(s)
			line := s.Line(bottom)
			col := len([]rune(line[:strings.Index(line, "WALL-")]))

			mousePress(t, term, col, bottom, tuitest.MouseLeft, 0)
			for r := bottom - 1; r >= top; r-- {
				mouseMotion(t, term, col, r, tuitest.MouseLeft, 0)
			}
			mouseMotion(t, term, col, top-1, tuitest.MouseLeft, 0)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				n := oldestWall(s)
				return n >= 0 && n < before-10
			}, uiTimeout); err != nil {
				t.Fatalf("the view never scrolled back past WALL-%d: %v\n%s", before, err, term.Snapshot())
			}

			mid := (top + bottom) / 2
			frames := sampleFlash(t, term, top, bottom, mid, func() {
				mouseRelease(t, term, col, top-1, tuitest.MouseLeft, 0)
			})
			logFlash(t, frames)
			midLine := term.Screen().Line(mid)
			loc := wallLine.FindStringIndex(midLine)
			if loc == nil {
				t.Fatalf("row %d holds no WALL line\n%s", mid, term.Snapshot())
			}
			checkSmoothSweep(t, frames, len([]rune(midLine[:loc[0]])), len([]rune(midLine[:loc[1]]))-1)
		})
	}
}
