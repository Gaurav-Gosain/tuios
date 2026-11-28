package tuie2e

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// pointerReporter is a pane program that tracks all motion in SGR cells (1003
// and 1006) and prints each report on its own line as
// <tag><button>;<x>;<y><M or m>. With capture it also asks tuios for pointer
// capture (OSC 7731). The key x turns capture off again.
func pointerReporter(tag string, capture bool) string {
	on := ""
	if capture {
		on = `printf "\033]7731;tuios-pointer-capture;on\007"; `
	}
	return `bash -c 'stty -echo -icanon; printf "\033[?1003h\033[?1006h"; ` + on +
		`echo ` + tag + `""ON; r=; while IFS= read -r -n1 c; do case $c in ` +
		`x) printf "\033]7731;tuios-pointer-capture;off\007"; echo ` + tag + `""OFF;; ` +
		`M|m) printf "` + tag + `%s.\n" "${r##*<}$c"; r=;; ` +
		`*) r=$r$c;; esac; done'`
}

// pointerReport is one line pointerReporter printed.
type pointerReport struct {
	button, x, y int
	release      bool
}

// pointerReports reads every report with tag off the screen, top to bottom.
func pointerReports(s tuitest.Screen, tag string) []pointerReport {
	re := regexp.MustCompile(regexp.QuoteMeta(tag) + `(\d+);(-?\d+);(-?\d+)([Mm])\.`)
	var out []pointerReport
	for _, m := range re.FindAllStringSubmatch(s.Text(), -1) {
		b, _ := strconv.Atoi(m[1])
		x, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		out = append(out, pointerReport{button: b, x: x, y: y, release: m[4] == "m"})
	}
	return out
}

// waitPointerReport waits for the report want with tag.
func waitPointerReport(t *testing.T, term *tuitest.Terminal, what, tag string, want pointerReport) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return slices.Contains(pointerReports(s, tag), want)
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the pane never got %+v: %v\n%s", what, want, err, term.Snapshot())
	}
}

// TestPointerCaptureKeepsTheDragInItsPane covers pointer capture (OSC 7731).
//
// Two tiled panes sit side by side. The right one runs a reporter that asks for
// capture, the left one a reporter that does not. A drag from the right pane
// into the left one must go to the right pane all the way, with x below zero
// once the pointer is past its left edge, and end there with the release. The
// left pane must get nothing. While the right pane has focus, bare motion over
// the left pane goes to it too.
//
// Then the right program turns capture off. The same kind of drag stops at
// the edge, as it did before capture existed: no report past the edge and no
// release, and still nothing in the left pane.
func TestPointerCaptureKeepsTheDragInItsPane(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
	}{{"standalone", false}, {"daemon", true}} {
		t.Run(tc.name, func(t *testing.T) {
			term, _ := start(t, startOpts{
				cols: 160, rows: 45,
				daemonDefault: tc.daemon,
				env:           []string{"SHELL=/bin/bash"},
			})
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 2, "pointer capture setup")
			enableTiling(t, term)

			// Tiling puts the second pane on the right, with the focus.
			enterTerminalMode(t, term)
			runInShell(t, term, pointerReporter("R", true), "RON", shellTimeout)
			clickToType(t, term, 20, 20)
			runInShell(t, term, pointerReporter("L", false), "LON", shellTimeout)

			// A click focuses the right pane and gives its cell under (120, 20).
			mouseClick(t, term, 120, 20, tuitest.MouseLeft, 0)
			var x0, y0 int
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				for _, r := range pointerReports(s, "R") {
					if r.button == 0 && r.release {
						x0, y0 = r.x, r.y
						return true
					}
				}
				return false
			}, uiTimeout); err != nil {
				t.Fatalf("the click never reached the right pane: %v\n%s", err, term.Snapshot())
			}

			// The drag goes 100 columns to the left, into the left pane.
			mouseDrag(t, term, 120, 20, 20, 20, tuitest.MouseLeft, 0)
			waitPointerReport(t, term, "captured release", "R", pointerReport{button: 0, x: x0 - 100, y: y0, release: true})
			waitPointerReport(t, term, "captured motion", "R", pointerReport{button: 32, x: x0 - 99, y: y0})
			if x0-100 >= 0 {
				t.Fatalf("the drag ended at x %d, which is not past the left edge", x0-100)
			}

			// Bare motion over the left pane, with the right pane focused.
			mouseHover(t, term, 30, 20)
			waitPointerReport(t, term, "captured hover", "R", pointerReport{button: 35, x: x0 - 90, y: y0})
			saveFrame(t, term, "pointer-capture-"+tc.name+"-on")

			// Capture off: the same drag five rows lower stops at the edge.
			if err := term.SendKeys("x"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("ROFF", uiTimeout); err != nil {
				t.Fatalf("the program never turned capture off: %v\n%s", err, term.Snapshot())
			}
			mouseDrag(t, term, 120, 25, 20, 25, tuitest.MouseLeft, 0)
			// A press at the edge and its release show that every report of
			// the drag has been read.
			mouseClick(t, term, 120, 30, tuitest.MouseLeft, 0)
			waitPointerReport(t, term, "click after the drag", "R", pointerReport{button: 0, x: x0, y: y0 + 10, release: true})
			s := term.Screen()
			for _, r := range pointerReports(s, "R") {
				if r.y == y0+5 && (r.x < 1 || r.release) {
					t.Fatalf("without capture the pane got %+v, past its edge or a release\n%s", r, term.Snapshot())
				}
			}
			if got := pointerReports(s, "L"); len(got) != 0 {
				t.Fatalf("the left pane got reports it never asked for: %+v\n%s", got, term.Snapshot())
			}
			if !strings.Contains(s.Text(), fmt.Sprintf("R32;1;%d", y0+5)) {
				t.Fatalf("the drag without capture never reached the edge of the pane\n%s", term.Snapshot())
			}
			saveFrame(t, term, "pointer-capture-"+tc.name+"-off")
		})
	}
}
