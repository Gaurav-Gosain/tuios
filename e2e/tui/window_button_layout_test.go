package tuie2e

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The window controls as they reach the real screen: the pill and the dots, at
// either end of the title bar, in the default glyphs and in ASCII. Each case
// pins the cells the controls are drawn on and then presses every control on
// its own glyph, so a layout change that moves the glyphs without moving the
// click targets (or the other way round) fails here.

type controlAction string

const (
	ctlClose    controlAction = "close"
	ctlMinimize controlAction = "minimize"
	ctlZoom     controlAction = "zoom"
)

// controlRun is one window's controls as found on screen.
type controlRun struct {
	row        int
	start, end int // first and last cell of the run, inclusive
	text       string
	at         map[controlAction]int // the column each control's glyph is on
}

// pillMarks maps the glyph a pill control is drawn as to what it does, across
// the default, unicode and ASCII glyphs.
var pillMarks = map[string]controlAction{
	"✕": ctlClose, "×": ctlClose, "X": ctlClose,
	"□": ctlZoom, "O": ctlZoom,
	"-": ctlMinimize, "−": ctlMinimize,
}

var (
	pillOpenCaps  = map[string]bool{"": true, "▏": true, "[": true}
	pillCloseCaps = map[string]bool{"": true, "▕": true, "]": true}
	// dotTriples are the three dots at rest and while hovered, where each
	// shows the symbol of what it does.
	dotTriples = [][3]string{{"●", "●", "●"}, {"⊗", "⊖", "⊕"}, {"o", "o", "o"}, {"x", "-", "+"}}
	topLeft    = map[string]bool{"╭": true, "┌": true, "┏": true, "╔": true, "+": true}
	topRight   = map[string]bool{"╮": true, "┐": true, "┓": true, "╗": true, "+": true}
)

func rowCells(s tuitest.Screen, y int) []string {
	cols, _ := s.Size()
	cells := make([]string, cols)
	for x := range cols {
		c := s.Cell(x, y).Content
		if c == "" {
			c = " "
		}
		cells[x] = c
	}
	return cells
}

// findPills returns every run that opens with a pill cap, closes with one
// within a few cells, and holds nothing but spaces and control marks. A title
// badge wears the same caps and is left out because it holds letters.
func findPills(s tuitest.Screen) []controlRun {
	_, rows := s.Size()
	var out []controlRun
	for y := range rows {
		cells := rowCells(s, y)
		for x := 0; x < len(cells); x++ {
			if !pillOpenCaps[cells[x]] {
				continue
			}
			for e := x + 2; e < len(cells) && e <= x+14; e++ {
				if !pillCloseCaps[cells[e]] {
					continue
				}
				at := map[controlAction]int{}
				ok := true
				for i := x + 1; i < e; i++ {
					if a, isMark := pillMarks[cells[i]]; isMark {
						at[a] = i
					} else if cells[i] != " " {
						ok = false
						break
					}
				}
				if ok && len(at) >= 2 {
					out = append(out, controlRun{row: y, start: x, end: e, text: strings.Join(cells[x:e+1], ""), at: at})
					x = e
				}
				break
			}
		}
	}
	return out
}

// findDots returns every run of three discs one cell apart. The dots are close,
// minimize and zoom from left to right at either end of the bar.
func findDots(s tuitest.Screen) []controlRun {
	_, rows := s.Size()
	var out []controlRun
	for y := range rows {
		cells := rowCells(s, y)
		for x := 1; x+5 < len(cells); x++ {
			tri := [3]string{cells[x], cells[x+2], cells[x+4]}
			if cells[x-1] == " " && cells[x+1] == " " && cells[x+3] == " " && cells[x+5] == " " && slices.Contains(dotTriples, tri) {
				out = append(out, controlRun{
					row: y, start: x - 1, end: x + 5,
					text: strings.Join(cells[x-1:x+6], ""),
					at:   map[controlAction]int{ctlClose: x, ctlMinimize: x + 2, ctlZoom: x + 4},
				})
				x += 4
			}
		}
	}
	return out
}

type buttonLayoutCase struct {
	name, style, position string
	ascii                 bool
	// want is the run of cells the controls take, caps included.
	want string
}

var buttonLayoutCases = []buttonLayoutCase{
	{"pill-left", "pill", "left", false, " ✕ □ - "},
	{"pill-right", "pill", "right", false, " - □ ✕ "},
	{"dots-left", "dots", "left", false, " ● ● ● "},
	{"dots-right", "dots", "right", false, " ● ● ● "},
	{"pill-left-ascii", "pill", "left", true, "[ X O - ]"},
	{"pill-right-ascii", "pill", "right", true, "[ - O X ]"},
	{"dots-left-ascii", "dots", "left", true, " o o o "},
}

func (c buttonLayoutCase) find(s tuitest.Screen) []controlRun {
	if c.style == "dots" {
		return findDots(s)
	}
	return findPills(s)
}

// waitRuns waits until exactly n windows' controls are on screen.
func (c buttonLayoutCase) waitRuns(t *testing.T, term *tuitest.Terminal, n int, what string) []controlRun {
	t.Helper()
	var runs []controlRun
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		runs = c.find(s)
		return len(runs) == n
	}, uiTimeout); err != nil {
		t.Fatalf("%s: want the controls of %d windows on screen, found %d: %v\n%s",
			what, n, len(runs), err, term.Snapshot())
	}
	return runs
}

// TestWindowButtonPillFillOnScreen checks that the pill is one filled run at
// every colour depth, on a dark and a light theme and on the terminal's own
// colours: the cells between the caps share one background that is not the
// terminal default, so the lead-in cell and the gaps are part of the pill.
func TestWindowButtonPillFillOnScreen(t *testing.T) {
	depths := []struct {
		name string
		env  []string
	}{
		{"16", []string{"TERM=xterm", "COLORTERM="}},
		{"256", []string{"TERM=xterm-256color", "COLORTERM="}},
		{"truecolor", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
	}
	for _, d := range depths {
		for _, th := range []string{"", "dracula", "atom_one_light"} {
			for _, pos := range []string{"left", "right"} {
				t.Run(d.name+"/"+th+"/"+pos, func(t *testing.T) {
					t.Parallel()
					args := []string{"--window-button-style", "pill", "--window-button-position", pos}
					if th != "" {
						args = append(args, "--theme", th)
					}
					term, _ := start(t, startOpts{cols: 80, rows: 24, args: args, env: d.env})
					waitBoot(t, term)
					newWindow(t, term)
					c := buttonLayoutCase{style: "pill"}
					run := c.waitRuns(t, term, 1, "one window")[0]
					s := term.Screen()
					fill := s.Cell(run.start+1, run.row)
					for x := run.start + 1; x < run.end; x++ {
						cell := s.Cell(x, run.row)
						if cell.Bg != fill.Bg || cell.Reverse != fill.Reverse {
							t.Errorf("pill cell %d (%q) has ground %+v reverse %v, the pill's first cell has %+v reverse %v\n%s",
								x-run.start, cell.Content, cell.Bg, cell.Reverse, fill.Bg, fill.Reverse, term.SnapshotStyled())
						}
					}
					if fill.Bg.Kind == tuitest.ColorDefault && !fill.Reverse {
						t.Errorf("the pill has no fill of its own\n%s", term.SnapshotStyled())
					}
				})
			}
		}
	}
}

// TestWindowButtonLayoutOnScreen pins the title bar's controls for every style
// and end, and presses each control on the glyph that names it.
func TestWindowButtonLayoutOnScreen(t *testing.T) {
	for _, c := range buttonLayoutCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			args := []string{
				"--window-button-style", c.style,
				"--window-button-position", c.position,
				"--window-title-position", "top",
			}
			if c.ascii {
				args = append(args, "--ascii-only")
			}
			term, _ := start(t, startOpts{cols: 100, rows: 30, args: args})
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 2, "two tiled windows")

			runs := c.waitRuns(t, term, 2, "two windows")
			s := term.Screen()
			t.Logf("top border: %q", strings.TrimRight(s.Line(runs[0].row), " "))

			for _, r := range runs {
				if r.text != c.want {
					t.Errorf("controls drawn as %q, want %q", r.text, c.want)
				}
				cells := rowCells(s, r.row)
				if c.position == "left" && !topLeft[cells[r.start-1]] {
					t.Errorf("left controls start at column %d, not next to the corner: %q", r.start, s.Line(r.row))
				}
				if c.position == "right" && !topRight[cells[r.end+1]] {
					t.Errorf("right controls end at column %d, not next to the corner: %q", r.end, s.Line(r.row))
				}
			}
			if t.Failed() {
				t.FailNow()
			}

			// Each press below lands on the glyph's own cell. The sequence
			// checks itself: only two zoom presses bring the split back, and
			// only a minimize leaves no window on screen after the close.
			stillTwo := func(what string) {
				t.Helper()
				if n := countWindows(term.Screen()); n != 2 {
					t.Fatalf("%s changed the window count to %d\n%s", what, n, term.Snapshot())
				}
			}

			// Zoom: one window fills the screen, so one set of controls is
			// left. The same control on that window puts the split back.
			first := runs[0]
			leftClick(t, term, first.at[ctlZoom], first.row)
			zoomed := c.waitRuns(t, term, 1, "after pressing zoom")
			stillTwo("zoom")
			if zoomed[0].text != c.want {
				t.Errorf("zoomed window's controls drawn as %q, want %q", zoomed[0].text, c.want)
			}
			t.Logf("zoomed top border: %q", strings.TrimRight(term.Screen().Line(zoomed[0].row), " "))
			leftClick(t, term, zoomed[0].at[ctlZoom], zoomed[0].row)
			runs = c.waitRuns(t, term, 2, "after pressing zoom again")
			stillTwo("zoom")

			// Minimize: the window leaves the screen and stays in the session.
			first = runs[0]
			leftClick(t, term, first.at[ctlMinimize], first.row)
			rest := c.waitRuns(t, term, 1, "after pressing minimize")
			stillTwo("minimize")

			// Close: the window is gone from the session, and the minimized
			// one stays off screen.
			leftClick(t, term, rest[0].at[ctlClose], rest[0].row)
			waitWindowCount(t, term, 1, "after pressing close")
			c.waitRuns(t, term, 0, "after pressing close")
		})
	}
}
