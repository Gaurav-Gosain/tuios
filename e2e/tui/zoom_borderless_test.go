package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	zoomBorderlessCols = 100
	zoomBorderlessRows = 30
)

const borderGlyphs = "╭╮╰╯┌┐└┘│─━┃"

var shellSizeRe = regexp.MustCompile(`SZ([0-9]+)x([0-9]+)`)

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
		extra      string
		floating   bool
		rail       bool
	}{
		{name: "borderless", borderless: true},
		{name: "borderless/daemon", borderless: true, daemon: true},
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

			dragSelect(t, term, left, left+len(answer)-1, 0)
			waitClipboardSequence(t, term, out, 0, answer)

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

			leaveTerminalMode(t, term)
			if err := term.SendKeys("z"); err != nil {
				t.Fatalf("send z: %v", err)
			}
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

// Closing the zoomed pane hands the zoom to the pane that takes the focus
// (#1626). Three panes, the focused one zoomed and closed: the pane that is
// focused next must fill the whole region, which a tiled pane of two never
// does. The shell size is the probe, as in TestZoomBorderless.
//
// Negative control: see NEGATIVE_CONTROLS.md, "Zoom survives a close".
func TestZoomSurvivesClose(t *testing.T) {
	for _, daemon := range []bool{true, false} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[startup]\ntiled = true\n[appearance]\nzoom_size = 100\nzoom_borderless = true\n[appearance.sidebar]\nenabled = false\n")
			o := startOpts{cols: zoomBorderlessCols, rows: zoomBorderlessRows}
			var term *tuitest.Terminal
			if daemon {
				if out, err := tuiosCLI(t, base, "new", "zoomclose", "--detach"); err != nil {
					t.Fatalf("create session: %v: %s", err, out)
				}
				term = attachIn(t, base, "zoomclose", o)
			} else {
				term = startIn(t, base, o)
				waitBoot(t, term)
				newWindow(t, term)
			}
			newWindow(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 3, "three panes")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the frame never settled: %v", err)
			}
			if err := term.SendKeys("z"); err != nil {
				t.Fatalf("send z: %v", err)
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the frame never settled: %v", err)
			}
			if err := term.SendKeys("x"); err != nil {
				t.Fatalf("send x: %v", err)
			}
			waitWindowCount(t, term, 2, "two panes after the close")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the frame never settled: %v", err)
			}
			enterTerminalMode(t, term)
			if err := term.SendKeys(`clear; printf 'SZ%sx%s\n' "$(tput lines)" "$(tput cols)"`, tuitest.Enter); err != nil {
				t.Fatalf("type the size probe: %v", err)
			}
			var rows, cols int
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				m := shellSizeRe.FindStringSubmatch(s.Text())
				if m == nil {
					return false
				}
				rows, _ = strconv.Atoi(m[1])
				cols, _ = strconv.Atoi(m[2])
				return true
			}, uiTimeout); err != nil {
				t.Fatalf("the shell never reported its size: %v\n%s", err, term.Snapshot())
			}
			saveArtifact(t, term, artifactDir(t), "after-close")
			if want := dockTop(term.Screen()); rows != want || cols != zoomBorderlessCols {
				t.Fatalf("the pane after the close is %dx%d, want the zoomed %dx%d\n%s", cols, rows, zoomBorderlessCols, want, term.Snapshot())
			}
		})
	}
}
