package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.zoom_borderless: a zoomed pane takes the whole pane region, draws
// no border and no title bar, and its shell is told the full size. Zooming out
// gives the pane its border back.
//
// How this could pass wrongly, written down first:
//   - The zoom could fail to come on, and an unzoomed pane with no border is
//     not what is being tested. The shell's size must be the region's, which
//     no tile of a two-pane layout is.
//   - The region could be measured off the frame under test, so a pane that
//     kept its border would shrink the region with it. The rows are read off
//     the dock and the left edge off the rail, never off the pane.
//   - The border check could look at rows where no border is ever drawn. The
//     boxed run, zoom_borderless off, must find border corners on the same
//     rows and a shell two cells smaller each way. That is the positive half.
//     Every borderless run must also find the pane's edge next to its text
//     again after zooming out, which a floating pane only gets back if the
//     zoom hands its border back.
//   - The frame could show no border while the pointer still treats the first
//     cell as one. A drag from the region's first cell must copy the text
//     there and nothing else.
//   - The shell size could be read off the echo of the typed command. The
//     marker is matched by a pattern the command does not contain.
//
// Negative control: see NEGATIVE_CONTROLS.md, "Borderless zoom".

const (
	zoomBorderlessCols = 100
	zoomBorderlessRows = 30
)

// borderGlyphs are the corners and edges a pane's own border is drawn with,
// in every glyph set the default looks can pick.
const borderGlyphs = "╭╮╰╯┌┐└┘│─━┃"

// shellSizeRe is the size the pane's shell reports, printed as SZ<rows>x<cols>.
var shellSizeRe = regexp.MustCompile(`SZ([0-9]+)x([0-9]+)`)

// dockTop is the first row of the dock, which is at the bottom: the lowest run
// of rows that start with the dock's rule or carry its window count.
func dockTop(s tuitest.Screen) int {
	_, rows := s.Size()
	top := rows
	for y := rows - 1; y >= max(0, rows-3); y-- {
		line := strings.TrimSpace(s.Line(y))
		if dockStatusRe.MatchString(line) || strings.HasPrefix(line, "────") {
			top = y
		}
	}
	return top
}

// regionCols is the part of a screen row right of the rail: the columns from
// left on.
func regionCols(line string, left int) string {
	r := []rune(line)
	if left >= len(r) {
		return ""
	}
	return string(r[left:])
}

func TestZoomBorderless(t *testing.T) {
	runs := []struct {
		name       string
		borderless bool
		daemon     bool
		// extra is more [appearance] or other config, after the zoom keys.
		extra string
		// floating starts the panes untiled.
		floating bool
		// rail shows the sidebar, so the region does not start at column 0.
		rail bool
	}{
		{name: "borderless", borderless: true},
		{name: "borderless/daemon", borderless: true, daemon: true},
		// Shared borders alone already drop the border of a zoom of the whole
		// screen, so this run zooms to part of it: without the option that is
		// the camera over the layout, with it the whole region.
		{name: "borderless/shared", borderless: true, extra: "shared_borders = true\nzoom_size = 90\n"},
		{name: "borderless/max-width", borderless: true, extra: "zoom_max_width = 60\n"},
		{name: "borderless/light", borderless: true, extra: "theme = \"catppuccin_latte\"\n"},
		{name: "borderless/floating", borderless: true, floating: true},
		{name: "borderless/rail", borderless: true, rail: true},
		{name: "boxed", borderless: false},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			base := t.TempDir()
			zoomSize := "zoom_size = 100\n"
			if strings.Contains(r.extra, "zoom_size") {
				zoomSize = ""
			}
			cfg := fmt.Sprintf("[startup]\ntiled = %v\n[appearance]\n%szoom_borderless = %v\n%s",
				!r.floating, zoomSize, r.borderless, r.extra)
			cfg += fmt.Sprintf("[appearance.sidebar]\nenabled = %v\n", r.rail)
			writeConfig(t, base, cfg)
			out := &lockedBuffer{}
			o := startOpts{cols: zoomBorderlessCols, rows: zoomBorderlessRows, out: out}
			var term *tuitest.Terminal
			if r.daemon {
				if out, err := tuiosCLI(t, base, "new", "zoomless", "--detach"); err != nil {
					t.Fatalf("create session: %v: %s", err, out)
				}
				term = attachIn(t, base, "zoomless", o)
			} else {
				term = startIn(t, base, o)
				waitBoot(t, term)
				newWindow(t, term)
			}
			newWindow(t, term)
			waitWindowCount(t, term, 2, "two panes")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the frame never settled: %v", err)
			}
			dir := artifactDir(t)
			saveArtifact(t, term, dir, "tiled")

			if err := term.SendKeys("z"); err != nil {
				t.Fatalf("send z: %v", err)
			}
			enterTerminalMode(t, term)
			// clear first, so the only text on the pane is the answer and the
			// prompt: a border check over rows of shell output would be a check
			// of the output.
			if err := term.SendKeys(`clear; printf 'SZ%sx%s\n' "$(tput lines)" "$(tput cols)"`, tuitest.Enter); err != nil {
				t.Fatalf("type the size probe: %v", err)
			}
			var rows, cols int
			var answer string
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				m := shellSizeRe.FindStringSubmatch(s.Text())
				if m == nil {
					return false
				}
				answer = m[0]
				rows, _ = strconv.Atoi(m[1])
				cols, _ = strconv.Atoi(m[2])
				return true
			}, uiTimeout); err != nil {
				t.Fatalf("the shell never reported its size: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the frame never settled: %v", err)
			}
			saveArtifact(t, term, dir, "zoomed")

			s := term.Screen()
			regionH := dockTop(s)
			if regionH >= zoomBorderlessRows || regionH < zoomBorderlessRows-3 {
				t.Fatalf("could not find the dock: its first row is %d of %d\n%s", regionH, zoomBorderlessRows, term.Snapshot())
			}
			// The region's left edge is where the rail ends. Without a rail it
			// is column 0. With one, the rail's own rule is the last column
			// that has a border glyph on every region row.
			left := 0
			if r.rail {
				left = -1
				for x := range zoomBorderlessCols / 2 {
					all := true
					for y := range regionH {
						rr := []rune(s.Line(y))
						if x >= len(rr) || !strings.ContainsRune("│┃▏▕|", rr[x]) {
							all = false
							break
						}
					}
					if all {
						left = x + 1
					}
				}
				if left <= 0 {
					t.Fatalf("the rail is not on screen\n%s", term.Snapshot())
				}
			}
			regionW := zoomBorderlessCols - left
			var borderRows []int
			for y := range regionH {
				if strings.ContainsAny(regionCols(s.Line(y), left), borderGlyphs) {
					borderRows = append(borderRows, y)
				}
			}

			if !r.borderless {
				// The positive half: the same probe on a boxed zoom finds the
				// border and a shell one cell in from each edge.
				if rows != regionH-2 || cols != regionW-2 {
					t.Fatalf("the boxed zoomed shell is %dx%d, want %dx%d\n%s", cols, rows, regionW-2, regionH-2, term.Snapshot())
				}
				if !strings.ContainsAny(s.Line(0), "╭┌") || !strings.ContainsAny(s.Line(regionH-1), "╰└") {
					t.Fatalf("the boxed zoom draws no border corners on rows 0 and %d\n%s", regionH-1, term.Snapshot())
				}
				return
			}
			if rows != regionH || cols != regionW {
				t.Fatalf("the zoomed shell is %dx%d, want the whole region %dx%d\n%s", cols, rows, regionW, regionH, term.Snapshot())
			}
			if len(borderRows) > 0 {
				t.Fatalf("border cells on rows %v of the zoomed region\n%s", borderRows, term.Snapshot())
			}
			if col := strings.Index(regionCols(s.Line(0), left), "SZ"); col != 0 {
				t.Fatalf("the shell's first column is at region column %d, want 0\n%s", col, term.Snapshot())
			}

			// The pointer reaches the pane's first cell, where a border would be:
			// a drag over the answer selects it and copies exactly it.
			dragSelect(t, term, left, left+len(answer)-1, 0)
			waitClipboardSequence(t, term, out, 0, answer)

			// The option applies at once, to a pane that is already zoomed:
			// off draws the border, on takes it away again.
			if r.daemon {
				hasBorder := func(s tuitest.Screen) bool {
					for y := range dockTop(s) {
						if strings.ContainsAny(regionCols(s.Line(y), left), borderGlyphs) {
							return true
						}
					}
					return false
				}
				for _, on := range []bool{false, true} {
					if out, err := tuiosCLI(t, base, "set-config", "appearance.zoom_borderless", strconv.FormatBool(on)); err != nil {
						t.Fatalf("set-config zoom_borderless %v: %v: %s", on, err, out)
					}
					if err := term.WaitFor(func(s tuitest.Screen) bool { return hasBorder(s) != on }, uiTimeout); err != nil {
						t.Fatalf("zoom_borderless set to %v at run time, border shown = %v: %v\n%s", on, hasBorder(term.Screen()), err, term.Snapshot())
					}
				}
			}

			// Zooming out gives the pane its border back. Under shared borders
			// that is the divider between the two panes, not a box of its own.
			leaveTerminalMode(t, term)
			if err := term.SendKeys("z"); err != nil {
				t.Fatalf("send z: %v", err)
			}
			// The cell left of the answer is the pane's own edge again.
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				for y := range dockTop(s) {
					line := s.Line(y)
					i := strings.Index(line, answer)
					if i <= 0 {
						continue
					}
					before := []rune(line[:i])
					return strings.ContainsRune(borderGlyphs, before[len(before)-1])
				}
				return false
			}, uiTimeout); err != nil {
				t.Fatalf("no border after zooming out: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the frame never settled: %v", err)
			}
			saveArtifact(t, term, dir, "unzoomed")
		})
	}
}
