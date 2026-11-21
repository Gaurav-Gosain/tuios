package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

var paneLabelGlyphRows = map[rune][5]string{
	'1': {" # ", "## ", " # ", " # ", "###"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'a': {"###", "# #", "###", "# #", "# #"},
	's': {"###", "#  ", "###", "  #", "###"},
}

func paneLabelGlyph(label string) [5]string {
	var rows [5]string
	for i, r := range label {
		g := paneLabelGlyphRows[r]
		for y := range 5 {
			if i > 0 {
				rows[y] += ".."
			}
			for _, c := range g[y] {
				if c == '#' {
					rows[y] += "##"
				} else {
					rows[y] += ".."
				}
			}
		}
	}
	return rows
}

func glyphMask(s tuitest.Screen) []string {
	cols, rows := s.Size()
	out := make([]string, rows)
	for y := range rows {
		row := make([]byte, cols)
		for x := range cols {
			row[x] = '.'
			c := s.Cell(x, y)
			if c.Content == "█" {
				row[x] = '#'
				continue
			}
			if c.Reverse || strings.TrimSpace(c.Content) != "" {
				continue
			}
			left, right := false, false
			for k := 1; k <= 14; k++ {
				if x-k >= 0 && s.Cell(x-k, y).Reverse {
					left = true
				}
				if x+k < cols && s.Cell(x+k, y).Reverse {
					right = true
				}
			}
			if left && right {
				row[x] = '#'
			}
		}
		out[y] = string(row)
	}
	return out
}

func findPaneLabel(s tuitest.Screen, label, name string) int {
	_, col := findPaneLabelAt(s, label, name)
	return col
}

func findPaneLabelAt(s tuitest.Screen, label, name string) (int, int) {
	mask := glyphMask(s)
	lines := strings.Split(s.Text(), "\n")
	glyph := paneLabelGlyph(label)
	match := func(row string, x int, want string) bool {
		if x < 1 || x+len(want) >= len(row) || row[x-1] == '#' || row[x+len(want)] == '#' {
			return false
		}
		return row[x:x+len(want)] == want
	}
	for y := 0; y+6 < len(mask) && y+6 < len(lines); y++ {
		for x := 1; x < len(mask[y]); x++ {
			ok := true
			for k := range 5 {
				if !match(mask[y+k], x, glyph[k]) {
					ok = false
					break
				}
			}
			if ok && (name == "" || strings.Contains(lines[y+6], name)) {
				return x, y
			}
		}
	}
	return -1, -1
}

func anyPaneLabel(s tuitest.Screen) bool {
	for _, row := range glyphMask(s) {
		if strings.Contains(row, "##") {
			return true
		}
	}
	return false
}

func paneLabelsSession(t *testing.T, config string, opts startOpts) (*tuitest.Terminal, string, []string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	if config != "" {
		writeConfig(t, base, config)
	}
	if o, err := tuiosCLI(t, base, "new", "e2e-labels", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, o)
	}
	for _, name := range []string{"beta", "gamma"} {
		if o, err := tuiosCLI(t, base, "new-window", name, "-s", "e2e-labels", "--no-focus"); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, o)
		}
	}
	ids := paneIDs(t, base, "e2e-labels")
	if o, err := tuiosCLI(t, base, "set-window", "-w", ids[0], "--name", "alpha", "-s", "e2e-labels"); err != nil {
		t.Fatalf("rename the first pane: %v\n%s", err, o)
	}
	opts.args = []string{"attach", "e2e-labels"}
	if opts.cols == 0 {
		opts.cols, opts.rows = 120, 40
	}
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 3 }, bootTimeout); err != nil {
		t.Fatalf("the three panes never showed: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enableTiling(t, term)
	return term, base, ids
}

func paneIDs(t *testing.T, base, session string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var payload struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode list-windows: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(payload.Windows))
	for _, w := range payload.Windows {
		ids = append(ids, w.ID)
	}
	return ids
}

func focusedPaneID(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var payload struct {
		Focused string `json:"focused_window_id"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode list-windows: %v\n%s", err, out)
	}
	return payload.Focused
}

func waitFocusedPane(t *testing.T, base, id, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		if got = focusedPaneID(t, base, "e2e-labels"); got == id {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: the focused pane is %s, want %s", what, got, id)
}

func focusPaneByCLI(t *testing.T, base, id string) {
	t.Helper()
	if o, err := tuiosCLI(t, base, "focus-window", id, "-s", "e2e-labels"); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, o)
	}
	waitFocusedPane(t, base, id, "focus-window")
	time.Sleep(300 * time.Millisecond)
}

func openPaneLabels(t *testing.T, term *tuitest.Terminal, label, name string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "Q")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return findPaneLabel(s, label, name) >= 0 }, uiTimeout); err != nil {
		t.Fatalf("label %q over %s never showed: %v\n%s", label, name, err, term.Snapshot())
	}
}

func waitPaneLabelsGone(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !anyPaneLabel(s) }, uiTimeout); err != nil {
		t.Fatalf("%s: the labels stayed up: %v\n%s", what, err, term.Snapshot())
	}
}

// TestPaneLabelsFocusAPane: with the default digit keys, every pane gets
// its number, 3 focuses the third pane, and Esc closes the labels without a
// jump.
func TestPaneLabelsFocusAPane(t *testing.T) {
	term, base, ids := paneLabelsSession(t, "", startOpts{})
	focusPaneByCLI(t, base, ids[0])

	openPaneLabels(t, term, "1", "alpha")
	for i, name := range []string{"beta", "gamma"} {
		if findPaneLabel(term.Screen(), fmt.Sprint(i+2), name) < 0 {
			t.Fatalf("no label %d over %s:\n%s", i+2, name, term.Snapshot())
		}
	}
	saveFrame(t, term, "pane-labels-digits")
	sendKeys(t, term, "3")
	waitPaneLabelsGone(t, term, "after 3")
	waitFocusedPane(t, base, ids[2], "label 3")

	openPaneLabels(t, term, "2", "beta")
	sendKeys(t, term, tuitest.Esc)
	waitPaneLabelsGone(t, term, "after esc")
	time.Sleep(300 * time.Millisecond)
	if got := focusedPaneID(t, base, "e2e-labels"); got != ids[2] {
		t.Fatalf("esc moved the focus to %s", got)
	}

	openPaneLabels(t, term, "2", "beta")
	if o, err := tuiosCLI(t, base, "new-window", "delta", "-s", "e2e-labels", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, o)
	}
	waitPaneLabelsGone(t, term, "after a pane opened")
}

// TestPaneLabelsTwoKeysAndCustomKeys: label_keys = "as" gives three panes
// the labels a, sa and ss. The first s waits for the second key, and the
// second s focuses the third pane.
func TestPaneLabelsTwoKeysAndCustomKeys(t *testing.T) {
	term, base, ids := paneLabelsSession(t, "[panes]\nlabel_keys = \"as\"\n", startOpts{})
	focusPaneByCLI(t, base, ids[0])

	openPaneLabels(t, term, "a", "alpha")
	for _, l := range []struct{ label, name string }{{"sa", "beta"}, {"ss", "gamma"}} {
		if findPaneLabel(term.Screen(), l.label, l.name) < 0 {
			t.Fatalf("no label %q over %s:\n%s", l.label, l.name, term.Snapshot())
		}
	}
	saveFrame(t, term, "pane-labels-two-keys")
	sendKeys(t, term, "s")
	time.Sleep(400 * time.Millisecond)
	if !anyPaneLabel(term.Screen()) {
		t.Fatalf("the first key of a two-key label closed the labels:\n%s", term.Snapshot())
	}
	if got := focusedPaneID(t, base, "e2e-labels"); got != ids[0] {
		t.Fatalf("the first key of a two-key label moved the focus to %s", got)
	}
	saveFrame(t, term, "pane-labels-typed")
	sendKeys(t, term, "s")
	waitPaneLabelsGone(t, term, "after s s")
	waitFocusedPane(t, base, ids[2], "label ss")
}

// TestPaneLabelsZoom: with a pane zoomed, the hidden panes are listed under
// the zoomed pane's label, and a label moves the zoom to its pane.
func TestPaneLabelsZoom(t *testing.T) {
	term, base, ids := paneLabelsSession(t, "", startOpts{})
	focusPaneByCLI(t, base, ids[0])

	sendKeys(t, term, tuitest.Ctrl('b'), "z")
	waitOnlyPane(t, term, "alpha", "the zoom never hid the other panes")
	openPaneLabels(t, term, "1", "alpha")
	waitScreen(t, term, "the hidden panes are not listed", "2  beta", "3  gamma")
	saveFrame(t, term, "pane-labels-zoom")
	sendKeys(t, term, "2")
	waitPaneLabelsGone(t, term, "after 2 in zoom")
	waitFocusedPane(t, base, ids[1], "label 2 in zoom")
	waitOnlyPane(t, term, "beta", "the zoom did not move to beta")
}

const labelPrompt = "LBL>"

func lastPaneLine(t *testing.T, base, id string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "capture-pane", "-w", id, "-s", "e2e-labels")
	if err != nil {
		t.Fatalf("capture-pane: %v\n%s", err, out)
	}
	last := ""
	for line := range strings.SplitSeq(out, "\n") {
		if l := strings.TrimRight(line, " "); strings.TrimSpace(l) != "" {
			last = l
		}
	}
	return last
}

func waitEveryPaneLine(t *testing.T, base string, ids []string, want, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		ok := true
		got := ""
		for _, id := range ids {
			if got = lastPaneLine(t, base, id); got != want {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: a pane's last line is %q, want %q", what, got, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func noPaneLine(t *testing.T, base string, ids []string, text, what string) {
	t.Helper()
	for _, id := range ids {
		if got := lastPaneLine(t, base, id); got == labelPrompt+" "+text {
			t.Fatalf("%s: %q reached pane %s", what, text, id)
		}
	}
}

// TestPaneLabelsKeepKeysAndPastesFromPanes: with every pane in multifocus
// and the labels up, neither the label key nor a paste reaches a shell. The
// positive half runs first in the same panes with the labels closed: the key
// and the paste reach the shells, and the line they leave is the one the
// check looks for.
func TestPaneLabelsKeepKeysAndPastesFromPanes(t *testing.T) {
	term, base, ids := paneLabelsSession(t, "", startOpts{})
	focusPaneByCLI(t, base, ids[0])
	for _, id := range ids {
		if o, err := tuiosCLI(t, base, "send-text", "-s", "e2e-labels", "-w", id, "PS1='"+labelPrompt+" '"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, o)
		}
		if o, err := tuiosCLI(t, base, "send-keys", "-s", "e2e-labels", "-w", id, "Enter"); err != nil {
			t.Fatalf("send-keys: %v\n%s", err, o)
		}
	}
	waitEveryPaneLine(t, base, ids, labelPrompt, "the prompt was never set")

	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "for multifocus")
	sendKeys(t, term, "Toggle multifocus on all panes")
	time.Sleep(200 * time.Millisecond)
	sendKeys(t, term, tuitest.Enter)
	waitPaletteClosed(t, term, "after multifocus")
	if err := term.WaitForText("Multifocus: 3 windows", uiTimeout); err != nil {
		t.Fatalf("the multifocus set never held three panes: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)

	sendKeys(t, term, "3")
	waitEveryPaneLine(t, base, ids, labelPrompt+" 3", "a key with the labels closed")
	sendKeys(t, term, tuitest.Ctrl('u'))
	waitEveryPaneLine(t, base, ids, labelPrompt, "ctrl+u after the key")
	if err := term.Paste("pz9"); err != nil {
		t.Fatalf("paste: %v", err)
	}
	waitEveryPaneLine(t, base, ids, labelPrompt+" pz9", "a paste with the labels closed")
	sendKeys(t, term, tuitest.Ctrl('u'))
	waitEveryPaneLine(t, base, ids, labelPrompt, "ctrl+u after the paste")

	openPaneLabels(t, term, "3", "gamma")
	if err := term.Paste("pz9"); err != nil {
		t.Fatalf("paste: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	noPaneLine(t, base, ids, "pz9", "a paste with the labels up")
	if !anyPaneLabel(term.Screen()) {
		t.Fatalf("a paste closed the labels:\n%s", term.Snapshot())
	}

	sendKeys(t, term, "3")
	time.Sleep(700 * time.Millisecond)
	noPaneLine(t, base, ids, "3", "the label key")
	waitPaneLabelsGone(t, term, "after 3 in multifocus")
	waitFocusedPane(t, base, ids[2], "label 3 in multifocus")
}

func panesDrawn(s tuitest.Screen) []string {
	var out []string
	for _, name := range []string{"alpha", "beta", "gamma"} {
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if strings.Contains(line, "╯") && strings.Contains(line, " "+name+" ") {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

func waitOnlyPane(t *testing.T, term *tuitest.Terminal, name, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		d := panesDrawn(s)
		return len(d) == 1 && d[0] == name
	}, uiTimeout); err != nil {
		t.Fatalf("%s: %v\n%s", what, err, term.Snapshot())
	}
}

// TestPaneLabelsLooks saves the labels as text, styled text and a PNG on the
// dark and the light look at every colour depth, for a person to look at,
// and checks that each label's glyphs are drawn in a colour of their own on
// a ground of their own.
func TestPaneLabelsLooks(t *testing.T) {
	looks := []chromeLook{lookDark, lookLatte}
	for _, look := range looks {
		for _, depth := range chromeDepths {
			t.Run(look.name+"-"+depth.name, func(t *testing.T) {
				cfg := ""
				if look.theme != "" {
					cfg = "[appearance]\ntheme = \"" + look.theme + "\"\n"
				}
				term, base, ids := paneLabelsSession(t, cfg, startOpts{shippedLooks: true, env: depth.env})
				focusPaneByCLI(t, base, ids[0])
				openPaneLabels(t, term, "1", "alpha")
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("the screen never settled: %v", err)
				}
				s := term.Screen()
				dir := artifactDir(t)
				saveArtifact(t, term, dir, "labels")
				savePNG(t, s, hostPalette(t, look.theme), dir, "labels")
				_ = os.WriteFile(filepath.Join(dir, "README"), []byte("display_panes over three tiled panes, then over a zoom\n"), 0o644)
				x, top := findPaneLabelAt(s, "1", "alpha")
				col := x + 2
				glyph := s.Cell(col, top)
				ground := s.Cell(col-2, top)
				if depth.name == "16" {
					if !ground.Reverse || glyph.Reverse {
						t.Fatalf("at 16 colours the box is not in reverse with the glyph a hole: box %+v glyph %+v", ground, glyph)
					}
				} else {
					if ground.Bg.Kind == tuitest.ColorDefault {
						t.Fatalf("the label has no ground of its own")
					}
					if glyph.Fg == ground.Bg {
						t.Fatalf("the glyph has no ink of its own: fg %+v on %+v", glyph.Fg, ground.Bg)
					}
				}

				sendKeys(t, term, tuitest.Esc)
				waitPaneLabelsGone(t, term, "after esc")
				sendKeys(t, term, tuitest.Ctrl('b'), "z")
				waitOnlyPane(t, term, "alpha", "the zoom never hid the other panes")
				openPaneLabels(t, term, "1", "alpha")
				waitScreen(t, term, "the hidden panes are not listed", "2  beta", "3  gamma")
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("the screen never settled: %v", err)
				}
				saveArtifact(t, term, dir, "zoom")
				savePNG(t, term.Screen(), hostPalette(t, look.theme), dir, "zoom")
			})
		}
	}
}
