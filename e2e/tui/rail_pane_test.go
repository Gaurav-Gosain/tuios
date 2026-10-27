package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// The rail's pane section ([appearance.sidebar.pane]), driven on a real
// daemon and a real client: a small program runs in a pane the rail draws
// under its own heading.
//
// How this could pass wrongly, written down first:
//   - The ticks could be the main pane's output. The program runs only in the
//     rail pane, and every read is confined to the rail's columns.
//   - "Sized to the rail" could be the PTY's default size. The program prints
//     what stty reports, and the rail's 27 content columns and the 8 rows of
//     height are both far from 80 by 24.
//   - "Not in tiling" could hold only because the pane never started. The
//     window count is read after the rail shows the program's output, and
//     list-windows --rail must list it.
//   - The typed line could reach the main shell. It is checked inside the
//     rail, as the program's GOT: echo, which only the rail pane prints.
//   - "Keeps running while hidden" could be a pane restarted on show. The
//     counter must go on from where it was, not start again at zero.

// railPaneScript prints the size it was given, a counter, and the last line
// typed into it, redrawn every half second.
const railPaneScript = `i=0; got=""
while :; do
  printf '\033[H\033[2J'
  printf 'SZ %s\n' "$(stty size)"
  printf 'TICK-%d\n' "$i"
  [ -n "$got" ] && printf 'GOT:%s\n' "$got"
  i=$((i+1))
  if read -r -t 0.5 line; then got=$line; fi
done
`

var railTickRe = regexp.MustCompile(`TICK-(\d+)`)

// railTick is the counter the rail pane shows, or -1.
func railTick(s tuitest.Screen) int {
	row := railRowOf(s, "TICK-")
	if row < 0 {
		return -1
	}
	m := railTickRe.FindStringSubmatch(railLine(s, row))
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// railPaneWindows lists the session's windows, with the rail pane when rail
// is set.
func railPaneWindows(t *testing.T, base string, rail bool) []map[string]any {
	t.Helper()
	args := []string{"list-windows", "--json", "-s", "e2e"}
	if rail {
		args = append(args, "--rail")
	}
	out, err := tuiosCLI(t, base, args...)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var payload struct {
		Windows []map[string]any `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	return payload.Windows
}

func TestRailPaneSection(t *testing.T) {
	script := filepath.Join(t.TempDir(), "railpane.sh")
	if err := os.WriteFile(script, []byte(railPaneScript), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\nsections = \"sessions,terminals,pane\"\n\n" +
		"[appearance.sidebar.pane]\ntitle = \"railtv\"\nheight = 8\ncommand = \"bash " + script + "\"\n"
	term, base := railClient(t, "e2e", config, startOpts{cols: 120, rows: 40})
	frames := artifactDir(t)
	snap := func(name string) {
		saveArtifact(t, term, frames, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, name)
	}

	// The heading, then the program's own lines under it, in the rail.
	waitRailShows(t, term, "TICK-", "the program's output in the rail")
	s := term.Screen()
	head := railRowOf(s, "railtv")
	size := railRowOf(s, "SZ ")
	if head < 0 || size != head+1 {
		t.Fatalf("the pane's first line is not right under its heading (heading row %d, first line row %d)\n%s", head, size, term.Snapshot())
	}
	// stty size is rows then columns: the configured height, and the rail's
	// 28 columns less its edge rule.
	waitRailShows(t, term, "SZ 8 27", "the program sized to the rail")
	snap("rail-pane")

	// One pane in the layout, and the rail pane only when asked for. Tiling
	// is on, so a rail pane the tiler counted would halve the main pane.
	if out, err := tuiosCLI(t, base, "set-layout", "-s", "e2e", "--tiling", "true"); err != nil {
		t.Fatalf("turn tiling on: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if w := railPaneWindows(t, base, false); len(w) == 1 {
			if width, _ := w[0]["width"].(float64); width >= 80 {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if n := countWindows(term.Screen()); n != 1 {
		t.Fatalf("the dock counts %d windows; the rail pane must not be one\n%s", n, term.Snapshot())
	}
	plain := railPaneWindows(t, base, false)
	if len(plain) != 1 {
		t.Fatalf("list-windows lists %d windows, want 1 (the rail pane is left out): %v", len(plain), plain)
	}
	if w, _ := plain[0]["width"].(float64); w < 80 {
		t.Fatalf("the main pane is %v columns wide: tiling made room for the rail pane", w)
	}
	all := railPaneWindows(t, base, true)
	if len(all) != 2 {
		t.Fatalf("list-windows --rail lists %d windows, want 2: %v", len(all), all)
	}
	railCount := 0
	for _, w := range all {
		if r, _ := w["rail"].(string); strings.Contains(r, "railpane.sh") {
			railCount++
		}
	}
	if railCount != 1 {
		t.Fatalf("list-windows --rail marks %d windows as the rail pane, want 1: %v", railCount, all)
	}

	// A click gives the pane the keyboard, and a typed line reaches it.
	mouseClick(t, term, 5, size+1, tuitest.MouseLeft, 0)
	time.Sleep(insertGuard)
	if err := term.SendKeys("hello", tuitest.Enter); err != nil {
		t.Fatalf("type into the rail pane: %v", err)
	}
	waitRailShows(t, term, "GOT:hello", "the typed line reached the program")
	snap("rail-pane-typed")

	// alt+esc gives the keyboard back: what is typed next is not the pane's.
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("leave the rail pane: %v", err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("i", "zzz", tuitest.Enter); err != nil {
		t.Fatalf("type into the main pane: %v", err)
	}
	time.Sleep(time.Second)
	if railRowOf(term.Screen(), "GOT:zzz") >= 0 {
		t.Fatalf("a line typed after alt+esc reached the rail pane\n%s", term.Snapshot())
	}

	// Hiding the rail leaves the program running. Shown again, the counter
	// goes on from where it was.
	before := railTick(term.Screen())
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("back to window mode: %v", err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "b"); err != nil {
		t.Fatalf("hide the rail: %v", err)
	}
	waitRailLacks(t, term, "railtv", "the rail hidden")
	time.Sleep(2 * time.Second)
	if err := term.SendKeys(tuitest.Ctrl('b'), "b"); err != nil {
		t.Fatalf("show the rail: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railTick(s) > before+2 }, uiTimeout); err != nil {
		t.Fatalf("the counter did not go on from %d after the rail came back: %v\n%s", before, err, term.Snapshot())
	}
	snap("rail-pane-shown-again")
}
