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

const railPaneScript = `i=0; got=""; buf=""
while :; do
  printf '\033[H\033[2J'
  printf 'SZ %s\n' "$(stty size)"
  printf 'TICK-%d\n' "$i"
  [ -n "$got" ] && printf 'GOT:%s\n' "$got"
  i=$((i+1))
  if read -r -t 0.5 part; then got=$buf$part; buf=""; else buf=$buf$part; fi
done
`

var railTickRe = regexp.MustCompile(`TICK-(\d+)`)

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

	waitRailShows(t, term, "TICK-", "the program's output in the rail")
	s := term.Screen()
	head := railRowOf(s, "railtv")
	size := railRowOf(s, "SZ ")
	if head < 0 || size != head+1 {
		t.Fatalf("the pane's first line is not right under its heading (heading row %d, first line row %d)\n%s", head, size, term.Snapshot())
	}
	waitRailShows(t, term, "SZ 8 27", "the program sized to the rail")
	snap("rail-pane")

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

	mouseClick(t, term, 5, size+1, tuitest.MouseLeft, 0)
	if err := term.SendKeys("hello", tuitest.Enter); err != nil {
		t.Fatalf("type into the rail pane: %v", err)
	}
	waitRailShows(t, term, "GOT:hello", "the typed line reached the program")
	snap("rail-pane-typed")

	sendKeys(t, term, tuitest.Alt(tuitest.Esc))
	if err := term.SendKeys("i", "zzz", tuitest.Enter); err != nil {
		t.Fatalf("type into the main pane: %v", err)
	}
	time.Sleep(time.Second)
	if railRowOf(term.Screen(), "GOT:zzz") >= 0 {
		t.Fatalf("a line typed after alt+esc reached the rail pane\n%s", term.Snapshot())
	}

	before := railTick(term.Screen())
	leaveTerminalMode(t, term)
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

// TestRailPaneArrivalSaysNothing: the rail pane is no window the person made,
// so its arrival puts no "Window created" notice on the dock. It arrives on a
// push soon after an attach, and the notice it raised covered "Window
// management mode" from the alt+esc sent just before: TestRailPaneSection
// waited for that text and failed on CI when the push came second.
//
// The pane is made here with the rail-pane verb after the client has settled,
// which is the order CI hit, every time.
//
// Negative control: with windowCountForNotice and panesMadeForNotice counting
// the rail pane again, the dock says "Window created (2 total)" and the
// stayTrue fails.
func TestRailPaneArrivalSaysNothing(t *testing.T) {
	config := "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\n"
	term, base := railClient(t, "e2e", config, startOpts{cols: 120, rows: 40})
	params := `{"session":"e2e","key":"sleep 600","command":["sleep","600"],"width":27,"height":8}`
	if out, err := tuiosCLI(t, base, "call", "rail-pane", params); err != nil {
		t.Fatalf("make the rail pane: %v\n%s", err, out)
	}
	if all := railPaneWindows(t, base, true); len(all) != 2 {
		t.Fatalf("list-windows --rail lists %d windows after the rail-pane call, want 2: %v", len(all), all)
	}
	frames := artifactDir(t)
	snap := func() {
		saveArtifact(t, term, frames, "rail-pane-arrived")
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, "rail-pane-arrived")
	}
	stayTrue(t, "the dock says no window was created for the rail pane", 2*time.Second, func() (bool, string) {
		if strings.Contains(term.Screen().Text(), "Window created") {
			snap()
			return false, term.Snapshot()
		}
		return true, ""
	})
	snap()
}
