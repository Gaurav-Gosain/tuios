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

// The pane labels (display_panes, prefix Q), driven through a real client on
// a daemon session: open the labels, find each label's block glyphs on the
// screen with the pane's name under them, type a label, and read the focused
// pane back from list-windows.
//
// How these could pass wrongly, written down first:
//   - The focus could already be on the pane the label names. Each test
//     focuses another pane first, and checks that it holds the focus before
//     the label is typed.
//   - A glyph could be found where the pane's own text happens to draw block
//     characters. The shells print nothing of the kind, and a glyph counts
//     only with the pane's name two rows under it.
//   - The labels could stay up after the jump and still pass a focus check.
//     Every jump waits for the glyphs to go.
//   - A typed label key could reach a shell as well as the labels. The
//     multifocus test checks that no pane of the set shows the key.
//
// Negative controls, all confirmed red (see NEGATIVE_CONTROLS.md).

// paneLabelGlyphRows is the block font the labels draw, for the keys these
// tests use, as internal/app/pane_labels_render.go has it.
var paneLabelGlyphRows = map[rune][5]string{
	'1': {" # ", "## ", " # ", " # ", "###"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'a': {"###", "# #", "###", "# #", "# #"},
	's': {"###", "#  ", "###", "  #", "###"},
}

// paneLabelGlyph is a label as the screen shows it at double width.
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

// glyphMask is the screen with every filled cell of the block font as '#'
// and every other cell as '.', one byte a cell. A filled cell is a full
// block, or at 16 colours a hole in a box drawn in reverse video: a blank
// cell not in reverse with reverse cells close on both sides of it.
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

// findPaneLabel finds the label's glyphs with name two rows under them, and
// returns the glyphs' top row, or -1.
func findPaneLabel(s tuitest.Screen, label, name string) int {
	_, col := findPaneLabelAt(s, label, name)
	return col
}

// findPaneLabelAt is findPaneLabel that also returns the glyphs' first
// column.
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

// anyPaneLabel reports whether any block glyph is on the screen.
func anyPaneLabel(s tuitest.Screen) bool {
	for _, row := range glyphMask(s) {
		if strings.Contains(row, "##") {
			return true
		}
	}
	return false
}

// paneLabelsSession makes a session of three panes named alpha, beta and
// gamma, attaches a client to it with config, and turns tiling on. It
// returns the client, the isolation root and the pane ids in order.
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

// paneIDs is the session's pane ids in list order.
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

// focusedPaneID is the pane the session has focused.
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

// waitFocusedPane waits for the session to focus id.
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

// focusPaneByCLI focuses a pane from the CLI and waits for the client to
// agree.
func focusPaneByCLI(t *testing.T, base, id string) {
	t.Helper()
	if o, err := tuiosCLI(t, base, "focus-window", id, "-s", "e2e-labels"); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, o)
	}
	waitFocusedPane(t, base, id, "focus-window")
	time.Sleep(300 * time.Millisecond)
}

// openPaneLabels presses the leader and Q and waits for the label.
func openPaneLabels(t *testing.T, term *tuitest.Terminal, label, name string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "Q")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return findPaneLabel(s, label, name) >= 0 }, uiTimeout); err != nil {
		t.Fatalf("label %q over %s never showed: %v\n%s", label, name, err, term.Snapshot())
	}
}

// waitPaneLabelsGone waits for every glyph to leave the screen.
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

// TestPaneLabelsZoomAndMultifocus: with a pane zoomed, the hidden panes are
// listed under the zoomed pane's label, and a label moves the zoom to its
// pane. With every pane in multifocus, the label key focuses the pane and
// reaches no shell.
func TestPaneLabelsZoomAndMultifocus(t *testing.T) {
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
	sendKeys(t, term, tuitest.Ctrl('b'), "z")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return len(panesDrawn(s)) == 3 }, uiTimeout); err != nil {
		t.Fatalf("the zoom never ended: %v\n%s", err, term.Snapshot())
	}

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
	openPaneLabels(t, term, "3", "gamma")
	sendKeys(t, term, "3")
	waitPaneLabelsGone(t, term, "after 3 in multifocus")
	waitFocusedPane(t, base, ids[2], "label 3 in multifocus")
	time.Sleep(500 * time.Millisecond)
	for _, id := range ids {
		out, err := tuiosCLI(t, base, "capture-pane", "-w", id, "-s", "e2e-labels")
		if err != nil {
			t.Fatalf("capture-pane: %v\n%s", err, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), "$ 3") {
				t.Fatalf("the label key reached pane %s:\n%s", id, out)
			}
		}
	}
}

// panesDrawn is the names on the bottom borders of the panes drawn.
func panesDrawn(s tuitest.Screen) []string {
	var out []string
	for _, name := range []string{"alpha", "beta", "gamma"} {
		for _, line := range strings.Split(s.Text(), "\n") {
			if strings.Contains(line, "╯") && strings.Contains(line, " "+name+" ") {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// waitOnlyPane waits for name to be the one pane drawn, as a zoom draws it.
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
				// The 1's first filled cell is two cells in from the glyph's
				// left edge, and the box's ground is two cells left of that.
				col := x + 2
				glyph := s.Cell(col, top)
				ground := s.Cell(col-2, top)
				if depth.name == "16" {
					// The box is the accent in reverse video, and the glyph
					// is a hole in it.
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

				// The zoomed form: the hidden panes listed under the label.
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
